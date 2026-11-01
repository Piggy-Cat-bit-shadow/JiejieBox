#!/usr/bin/env python3
"""Offline behavioural tests: no GitHub calls, certificates or Apple uploads."""
import argparse
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location("apple_release", Path(__file__).with_name("apple-release.py"))
ar = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ar)


def candidate():
    return dict(schema_version=1, repository="owner/core", run_id=123, run_attempt=2,
                parent_sha="a" * 40, jj_version="0.1.36", release_tag="v0.1.36",
                apple_client_sha="b" * 40, apple_client_repository="https://github.com/owner/apple",
                apple_client_branch="main", build_ios=True, build_macos=True)


def manifest(c=None):
    c = c or candidate()
    return dict(parent_sha=c["parent_sha"], jj_version=c["jj_version"], run_id=str(c["run_id"]),
                run_attempt=str(c["run_attempt"]), apple_release=c)


def workflow(**changes):
    r = dict(id=123, run_attempt=2, path=ar.WORKFLOW, event="workflow_dispatch", head_sha="a" * 40,
             head_repository=dict(full_name="owner/core"), status="in_progress")
    r.update(changes)
    return r


def job(name, status="completed", conclusion="success"):
    return dict(name=name, status=status, conclusion=conclusion)


class ReleaseTests(unittest.TestCase):
    def test_archive_main_app_and_extension_must_share_release_version_and_build(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp) / "App.app"
            extension = app / "PlugIns/Tunnel.appex"
            extension.mkdir(parents=True)
            fields = dict(CFBundleShortVersionString="0.1.36", CFBundleVersion="456")
            for bundle in (app, extension):
                (bundle / "Info.plist").write_bytes(plistlib.dumps(fields))
            args = argparse.Namespace(app=app, version="0.1.36", build_number="456")
            ar.archive_version(args)
            for field, value in (("CFBundleShortVersionString", "0.1.35"), ("CFBundleVersion", "455")):
                (extension / "Info.plist").write_bytes(plistlib.dumps(dict(fields, **{field: value})))
                with self.subTest(field=field), self.assertRaises(ValueError):
                    ar.archive_version(args)

    def test_candidate_accepts_explicit_identity(self):
        self.assertEqual(ar.validate_candidate(candidate())["jj_version"], "0.1.36")

    def test_candidate_rejects_bad_and_missing_fields(self):
        changes = dict(parent_sha="main", apple_client_sha="latest", jj_version="0.1.36+sha",
                       release_tag="v0.1.35", repository="other", run_id="123", run_attempt=0,
                       apple_client_branch="main\nEVIL=1", build_ios="true",
                       apple_client_repository="https://evil.test/apple")
        for key, value in changes.items():
            with self.subTest(key=key), self.assertRaises(ValueError):
                ar.validate_candidate(dict(candidate(), **{key: value}))

    def test_no_platform_is_rejected(self):
        with self.assertRaises(ValueError):
            ar.validate_candidate(dict(candidate(), build_ios=False, build_macos=False))

    def test_run_must_be_the_same_workflow_repo_sha_and_attempt(self):
        changes = [dict(path=".github/workflows/other.yml"), dict(event="push"), dict(id=456),
                   dict(run_attempt=3), dict(head_sha="c" * 40), dict(head_repository=dict(full_name="fork/core"))]
        for changed in changes:
            with self.subTest(changed=changed), patch.object(ar, "api", return_value=workflow(**changed)), self.assertRaises(ValueError):
                ar.check_run("owner/core", 123, 2, "a" * 40)

    def test_live_run_can_supply_library_without_waiting_for_macos(self):
        with patch.object(ar, "api", return_value=workflow()):
            self.assertEqual(ar.check_run("owner/core", 123, 2)["status"], "in_progress")
        self.assertTrue(ar.check_jobs([job("libbox"), job("macos", "in_progress", None)], ["libbox"]))

    def test_ios_does_not_wait_for_or_accept_macos_as_its_gate(self):
        jobs = [job("libbox"), job("ios"), job("macos", conclusion="failure")]
        self.assertTrue(ar.check_jobs(jobs, ["libbox", "ios"]))
        with self.assertRaises(ValueError):
            ar.check_jobs(jobs, ["libbox", "macos"])

    def test_pending_missing_jobs_wait_but_terminal_missing_fails(self):
        self.assertFalse(ar.check_jobs([job("libbox")], ["libbox", "ios"]))
        self.assertFalse(ar.check_jobs([job("ios", "in_progress", None)], ["ios"]))
        with self.assertRaises(ValueError):
            ar.check_jobs([job("libbox")], ["libbox", "ios"], terminal=True)

    def test_job_failures_skips_and_duplicates_are_not_success(self):
        for conclusion in ("failure", "cancelled", "skipped", "timed_out", None):
            with self.subTest(conclusion=conclusion), self.assertRaises(ValueError):
                ar.check_jobs([job("ios", conclusion=conclusion)], ["ios"])
        with self.assertRaises(ValueError):
            ar.check_jobs([job("ios"), job("ios (duplicate)")], ["ios"])

    def test_manifest_identity_includes_version_and_attempt(self):
        for key in ("parent_sha", "jj_version", "run_id", "run_attempt"):
            with self.subTest(key=key), self.assertRaises(ValueError):
                ar.verify_manifest(dict(manifest(), **{key: "wrong"}), candidate())

    def test_bind_preserves_payload_hash_and_binds_client(self):
        with tempfile.TemporaryDirectory() as tmp:
            cp, mp = Path(tmp) / "candidate.json", Path(tmp) / "manifest.json"
            ar.write_json(cp, candidate())
            original = manifest()
            del original["apple_release"]
            original["artifact_sha256"] = "unchanged"
            ar.write_json(mp, original)
            ar.bind(argparse.Namespace(candidate=cp, manifest=mp))
            self.assertEqual(ar.read_json(mp)["artifact_sha256"], "unchanged")
            self.assertEqual(ar.read_json(mp)["apple_release"], candidate())

    def test_release_rejects_newest_run_substitution_and_requires_pin(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "manifest.json"
            ar.write_json(path, manifest())
            for rid, attempt, repo in (("", "", "owner/core"), ("124", "2", "owner/core"),
                                       ("123", "3", "owner/core"), ("123", "2", "other/core")):
                with self.subTest(rid=rid, attempt=attempt, repo=repo), self.assertRaises(ValueError):
                    ar.verify_release(argparse.Namespace(manifest=path, run_id=rid, attempt=attempt, repo=repo))
            ar.verify_release(argparse.Namespace(manifest=path, run_id="123", attempt="2", repo="owner/core"))

    def test_unpinned_historical_release_remains_supported(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "manifest.json"
            ar.write_json(path, {})
            args = argparse.Namespace(manifest=path, run_id="", attempt="", repo="")
            ar.verify_release(args)
            args.run_id = "123"
            with self.assertRaises(ValueError):
                ar.verify_release(args)

    def test_legacy_release_uses_frozen_apple_source_and_refuses_wrong_version(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "manifest.json"
            ar.write_json(path, manifest())
            args = argparse.Namespace(manifest=path, sha="a" * 40, version="0.1.36", repo="owner/core",
                                      apple_repository="https://github.com/owner/apple")
            with patch("sys.stdout", new_callable=io.StringIO) as output:
                ar.client_sha(args)
                self.assertEqual(output.getvalue().strip(), "b" * 40)
            args.version = "0.1.35"
            with self.assertRaises(ValueError):
                ar.client_sha(args)

    def test_tag_resolution_uses_peeled_commit(self):
        c = candidate()
        annotated = f"{'c' * 40}\trefs/tags/v0.1.36\n{'a' * 40}\trefs/tags/v0.1.36^{{}}"
        with patch.object(ar, "run", return_value=annotated):
            self.assertTrue(ar.tag_matches(".", c))
        with patch.object(ar, "run", return_value=""):
            self.assertFalse(ar.tag_matches(".", c))
        with patch.object(ar, "run", return_value=f"{'c' * 40}\trefs/tags/v0.1.36"), self.assertRaises(ValueError):
            ar.tag_matches(".", c)

    def test_artifact_selection_refuses_expired_and_duplicate(self):
        for entries in ([dict(name="lib", expired=True)], [dict(name="lib"), dict(name="lib")]):
            with patch.object(ar, "api", return_value=dict(total_count=len(entries), artifacts=entries)), self.assertRaises(ValueError):
                ar.artifact("owner/core", 123, "lib")

    def test_download_by_id_refuses_zip_traversal(self):
        for name in ("../outside", "nested/file", "..\\outside"):
            data = io.BytesIO()
            with zipfile.ZipFile(data, "w") as z:
                z.writestr(name, "bad")
            def downloaded(*args, **kwargs):
                kwargs["stdout"].write(data.getvalue())
            with tempfile.TemporaryDirectory() as tmp, patch.object(ar.subprocess, "run", side_effect=downloaded), self.assertRaises(ValueError):
                ar.download("owner/core", dict(id=7), tmp)

    def test_wait_timeout_is_bounded(self):
        with patch.object(ar.time, "monotonic", side_effect=[0, 100]), self.assertRaises(ValueError):
            ar.wait_for("test", lambda: False, 1, 30)

    def test_follow_never_runs_on_windows_or_dispatches(self):
        with patch.object(ar.sys, "platform", "win32"), self.assertRaises(ValueError):
            ar.follow(argparse.Namespace())

    def test_follow_builds_isolated_frozen_source_while_other_jobs_are_running(self):
        with tempfile.TemporaryDirectory() as tmp:
            controller = Path(tmp)
            calls = []
            def command(*args, **kwargs):
                calls.append(args)
                if args[:3] == ("git", "rev-parse", "--show-toplevel"):
                    return str(controller)
                if args[:3] == ("gh", "repo", "view"):
                    self.assertEqual(args[3], "https://github.com/owner/core.git")
                    return "owner/core"
                if args[:4] == ("git", "remote", "get-url", "origin"):
                    return "https://github.com/owner/core.git"
                if args[:3] == ("git", "worktree", "add"):
                    root = Path(args[4])
                    (root / "release").mkdir(parents=True)
                    (root / "release/JIEJIE_VERSION").write_text("0.1.36\n")
                if args[:2] == ("bash", "scripts/ci/apple-client-source.sh"):
                    return "clients/apple"
                return ""
            def downloaded(repo, item, target):
                target = Path(target)
                target.mkdir(parents=True)
                if item["id"] == 7:
                    ar.write_json(target / "apple-release.json", candidate())
                else:
                    ar.write_json(target / "manifest.json", dict(manifest(), artifact_sha256="hash"))
            def subprocess_run(args, **kwargs):
                calls.append(tuple(args))
                if args[1] == "scripts/release-apple.sh":
                    self.assertEqual(args[-1], "testflight-ios")
                    self.assertEqual(kwargs["env"]["APPLE_CLIENT_REF"], "b" * 40)
                    self.assertEqual(kwargs["env"]["APPLE_USE_PREBUILT_LIBBOX"], "1")
                    self.assertTrue(Path(kwargs["env"]["APPLE_RELEASE_CONTEXT"]).exists())
            args = argparse.Namespace(run_id=123, repo=None, platform="ios", interval=1, timeout=10)
            with patch.object(ar.sys, "platform", "darwin"), patch.object(ar, "run", side_effect=command), \
                 patch.object(ar, "check_run", return_value=workflow()), patch.object(ar, "tag_matches", return_value=False), \
                 patch.object(ar, "artifact", side_effect=[dict(id=7), dict(id=8)]), \
                 patch.object(ar, "jobs_for", return_value=[job("libbox"), job("ios", "in_progress", None), job("macos", "in_progress", None)]), \
                 patch.object(ar, "download", side_effect=downloaded), patch.object(ar.subprocess, "run", side_effect=subprocess_run):
                ar.follow(args)
            self.assertTrue(any(call[:3] == ("git", "worktree", "add") for call in calls))
            self.assertFalse(any("workflow" in call or "push" in call for call in calls))
            self.assertTrue(list((controller / "build/apple-follow").glob("*/published.json")))
            self.assertFalse(list((controller / "build/apple-follow").glob("*.lock")))

    def test_upload_waits_for_ios_and_tag_then_rechecks_local_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            lib = root / "build/apple-follow-libbox"
            lib.mkdir(parents=True)
            (lib / "Libbox.xcframework.tar.gz").write_bytes(b"library")
            digest = hashlib.sha256(b"library").hexdigest()
            ar.write_json(lib / "manifest.json", dict(manifest(), artifact_sha256=digest))
            context = root / "context.json"
            ar.write_json(context, dict(candidate=candidate(), platforms=["ios"], build_number="456",
                                        artifact_sha256=digest, timeout=100, interval=1))
            def waited(label, poll, *_):
                self.assertFalse(poll())  # CI passed but tag not created yet.
                self.assertTrue(poll())
            with patch.object(ar.Path, "cwd", return_value=root), patch.object(ar, "verify_local") as local, \
                 patch.object(ar, "check_run", return_value=workflow()), \
                 patch.object(ar, "jobs_for", return_value=[job("libbox"), job("ios"), job("macos", "in_progress", None)]), \
                 patch.object(ar, "tag_matches", side_effect=[False, True]), patch.object(ar, "wait_for", side_effect=waited), \
                 patch.dict(os.environ, APPLE_BUILD_NUMBER="456"):
                ar.upload_gate(argparse.Namespace(context=context, platform="ios"))
                self.assertEqual(local.call_count, 2)
            # Tampered bytes are rejected before any CI polling or upload can happen.
            (lib / "Libbox.xcframework.tar.gz").write_bytes(b"tampered")
            with patch.object(ar.Path, "cwd", return_value=root), patch.object(ar, "verify_local"), \
                 patch.dict(os.environ, APPLE_BUILD_NUMBER="456"), self.assertRaises(ValueError):
                ar.upload_gate(argparse.Namespace(context=context, platform="ios"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
