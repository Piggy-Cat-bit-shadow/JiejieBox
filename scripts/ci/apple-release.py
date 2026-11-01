#!/usr/bin/env python3
"""Freeze an Apple release in CI and follow it on the signing Mac.

GitHub access uses gh's existing authentication. This tool never dispatches a workflow,
pushes a ref, or changes the operator's checkout. All subprocess arguments are arrays.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile

WORKFLOW = ".github/workflows/client-apple.yml"
PENDING = {"queued", "in_progress", "waiting", "pending", "requested"}


def run(*args, cwd=None, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True).strip()


def api(repo, suffix):
    return json.loads(run("gh", "api", f"repos/{repo}/{suffix}"))


def require(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_candidate(c):
    require(isinstance(c, dict) and c.get("schema_version") == 1, "unsupported Apple release identity")
    for key in ("parent_sha", "apple_client_sha"):
        require(re.fullmatch(r"[0-9a-f]{40}", c.get(key, "")), f"invalid {key}")
    require(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", c.get("jj_version", "")), "invalid App release version")
    require(c.get("release_tag") == "v" + c["jj_version"], "release tag/version mismatch")
    require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", c.get("repository", "")), "invalid repository")
    require(re.fullmatch(r"https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:\.git)?",
                         c.get("apple_client_repository", "")), "invalid Apple repository")
    require(re.fullmatch(r"[A-Za-z0-9_./-]+", c.get("apple_client_branch", "")), "invalid Apple branch")
    for key in ("run_id", "run_attempt"):
        require(type(c.get(key)) is int and c[key] > 0, f"invalid {key}")
    for key in ("build_ios", "build_macos"):
        require(type(c.get(key)) is bool, f"invalid {key}")
    require(c["build_ios"] or c["build_macos"], "no Apple platform selected")
    return c


def candidate(args):
    version = Path("release/JIEJIE_VERSION").read_text().strip()
    c = validate_candidate(dict(
        schema_version=1, repository=os.environ["GITHUB_REPOSITORY"],
        run_id=int(os.environ["GITHUB_RUN_ID"]), run_attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]),
        parent_sha=run("git", "rev-parse", "HEAD"), jj_version=version, release_tag="v" + version,
        apple_client_sha=args.apple_sha, apple_client_repository=args.apple_repository,
        apple_client_branch=args.apple_branch, build_ios=args.build_ios == "true",
        build_macos=args.build_macos == "true"))
    write_json(args.out, c)


def check_run(repo, run_id, attempt=None, sha=None):
    r = api(repo, f"actions/runs/{run_id}")
    require(r.get("path") == WORKFLOW and r.get("event") == "workflow_dispatch",
            "the selected run is not a manually dispatched Apple client workflow")
    require(r.get("head_repository", {}).get("full_name", "").lower() == repo.lower(),
            "the run builds a different repository")
    require(r.get("id") == int(run_id), "GitHub returned a different run")
    if attempt is not None:
        require(r.get("run_attempt") == int(attempt),
                "the Apple run was re-run; its artifact identity changed. Start a new follow operation")
    if sha is not None:
        require(r.get("head_sha") == sha, "the Apple run builds a different core commit")
    return r


def check_jobs(jobs, required, terminal=False):
    """Return False while required jobs are pending; failures never become waits."""
    ready = True
    for name in required:
        matches = [j for j in jobs if j.get("name") == name or j.get("name", "").startswith(name + " ")]
        require(len(matches) <= 1, f"ambiguous {name} jobs")
        if not matches:
            require(not terminal, f"required {name} job is absent")
            ready = False
            continue
        j = matches[0]
        if j.get("status") != "completed":
            require(not terminal, f"required {name} job did not complete")
            ready = False
        else:
            require(j.get("conclusion") == "success", f"required {name} job: {j.get('conclusion')}")
    return ready


def jobs_for(repo, run_id, attempt):
    p = api(repo, f"actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100")
    require(p.get("total_count", 0) <= 100, "unexpectedly large Apple job list")
    return p["jobs"]


def artifact(repo, run_id, name):
    p = api(repo, f"actions/runs/{run_id}/artifacts?per_page=100")
    require(p.get("total_count", 0) <= 100, "unexpectedly large Apple artifact list")
    matches = [a for a in p["artifacts"] if a["name"] == name]
    require(len(matches) <= 1, f"ambiguous artifact {name}")
    if not matches:
        return None
    require(not matches[0].get("expired"), f"artifact {name} expired")
    return matches[0]


def download(repo, item, target):
    # Download by immutable artifact ID, rather than reselecting by name after the check.
    target = Path(target)
    target.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryFile() as archive:
        subprocess.run(["gh", "api", f"repos/{repo}/actions/artifacts/{item['id']}/zip"], stdout=archive, check=True)
        archive.seek(0)
        with zipfile.ZipFile(archive) as bundle:
            for entry in bundle.infolist():
                require(not entry.is_dir() and Path(entry.filename).name == entry.filename
                        and "\\" not in entry.filename and entry.filename not in (".", ".."),
                        "unexpected nested path in Apple artifact")
                with bundle.open(entry) as source, (target / entry.filename).open("wb") as destination:
                    shutil.copyfileobj(source, destination)


def wait_for(label, poll, timeout, interval):
    deadline = time.monotonic() + timeout
    print(label, flush=True)
    while True:
        result = poll()
        if result:
            return result
        require(time.monotonic() < deadline, f"timed out: {label}")
        print(f"  waiting; checking again in {interval}s", flush=True)
        time.sleep(interval)


def bind(args):
    c = validate_candidate(read_json(args.candidate))
    m = read_json(args.manifest)
    verify_manifest(m, c)
    m["apple_release"] = c
    write_json(args.manifest, m)


def verify_release(args):
    m = read_json(args.manifest)
    c = m.get("apple_release")
    # Historical releases have no early-publish identity. A caller pinning a run
    # must never accept a historical artifact as proof of that pin.
    if c is None:
        require(not args.run_id, "pinned Apple release has no frozen identity; build it with the current workflow")
        return
    validate_candidate(c)
    verify_manifest(m, c)
    require(args.run_id and args.attempt, "this Apple artifact requires apple_run_id and apple_run_attempt in the Release inputs")
    require(str(c["run_id"]) == args.run_id and str(c["run_attempt"]) == args.attempt,
            "Release selected a different Apple run/attempt from the TestFlight build")
    require(c["repository"].lower() == args.repo.lower(), "Apple artifact belongs to a different repository")


def archive_version(args):
    app = Path(args.app)
    bundles = [app] + sorted(app.rglob("*.appex"))
    for bundle in bundles:
        info = bundle / "Contents/Info.plist"
        if not info.exists():
            info = bundle / "Info.plist"
        with info.open("rb") as source:
            data = plistlib.load(source)
        require(data.get("CFBundleShortVersionString") == args.version,
                f"{bundle.name}: archived version differs from {args.version}")
        require(data.get("CFBundleVersion") == args.build_number,
                f"{bundle.name}: archived build number differs from {args.build_number}")


def client_sha(args):
    m = read_json(args.manifest)
    c = validate_candidate(m["apple_release"])
    verify_manifest(m, c)
    require(c["parent_sha"] == args.sha and c["jj_version"] == args.version,
            "Apple identity does not match the local release")
    require(c["repository"].lower() == args.repo.lower() and c["apple_client_repository"] == args.apple_repository,
            "Apple identity names a different core or client repository")
    print(c["apple_client_sha"])


def verify_manifest(m, c):
    for key in ("parent_sha", "jj_version"):
        require(m.get(key) == c[key], f"Libbox {key} differs from the frozen release")
    for key in ("run_id", "run_attempt"):
        require(str(m.get(key)) == str(c[key]), f"Libbox {key} differs from the frozen release")


def tag_matches(root, c):
    tag = c["release_tag"]
    rows = run("git", "ls-remote", "--tags", "origin", f"refs/tags/{tag}", f"refs/tags/{tag}^{{}}", cwd=root)
    refs = dict(line.split()[::-1] for line in rows.splitlines())
    sha = refs.get(f"refs/tags/{tag}^{{}}", refs.get(f"refs/tags/{tag}"))
    if not sha:
        return False
    require(sha == c["parent_sha"], f"{tag} points to a different commit; refusing TestFlight upload")
    return True


def verify_local(root, c, platforms):
    require(run("git", "rev-parse", "HEAD", cwd=root) == c["parent_sha"], "local core commit changed")
    require((Path(root) / "release/JIEJIE_VERSION").read_text().strip() == c["jj_version"],
            "local release version changed")
    state = Path(root) / "build/apple-client-source.env"
    fields = dict(line.split("=", 1) for line in state.read_text().splitlines() if "=" in line and not line.startswith("#"))
    require(fields.get("APPLE_CLIENT_SHA") == c["apple_client_sha"], "Apple source resolution changed")
    require(fields.get("APPLE_CLIENT_REPOSITORY") == c["apple_client_repository"], "Apple source repository changed")
    for platform in platforms:
        directory = run("bash", "scripts/ci/apple-client-source.sh", "dir", platform, cwd=root)
        require(run("git", "-C", directory, "rev-parse", "HEAD", cwd=root) == c["apple_client_sha"],
                f"{platform} source commit changed")


def upload_gate(args):
    context = read_json(args.context)
    c = validate_candidate(context["candidate"])
    require(args.platform in context["platforms"], "platform is not part of this follow operation")
    require(os.environ.get("APPLE_BUILD_NUMBER") == context["build_number"], "TestFlight build number changed")
    root = Path.cwd()
    verify_local(root, c, context["platforms"])
    lib = root / "build/apple-follow-libbox"
    m = read_json(lib / "manifest.json")
    require(m.get("apple_release") == c, "Libbox release identity changed")
    digest = file_sha256(lib / "Libbox.xcframework.tar.gz")
    require(digest == context["artifact_sha256"] == m["artifact_sha256"], "Libbox bytes changed")

    def ready():
        r = check_run(c["repository"], c["run_id"], c["run_attempt"], c["parent_sha"])
        producer = "libbox" if c["build_ios"] else "macos"
        required = list(dict.fromkeys((producer, args.platform)))
        jobs_ready = check_jobs(jobs_for(c["repository"], c["run_id"], c["run_attempt"]), required,
                                r["status"] == "completed")
        # Still read the tag while CI is pending: a conflicting tag is an immediate failure.
        tagged = tag_matches(root, c)
        return jobs_ready and tagged

    wait_for(f"Before {args.platform} upload: waiting for {c['release_tag']} and platform CI",
             ready, context["timeout"], context["interval"])
    # The archive and local selection must still match after a possibly long wait.
    verify_local(root, c, context["platforms"])
    print(f"PASS: {c['release_tag']} / core {c['parent_sha']} / Apple {c['apple_client_sha']}", flush=True)


def follow(args):
    require(sys.platform == "darwin", "--follow-run must be executed on the signing Mac")
    controller = Path(run("git", "rev-parse", "--show-toplevel")).resolve()
    # gh's default repository can be upstream in a fork with both remotes.
    # Resolve origin explicitly, including GitHub's old-name redirects.
    repo = args.repo or run("gh", "repo", "view", run("git", "remote", "get-url", "origin"),
                            "--json", "nameWithOwner", "-q", ".nameWithOwner")
    r = check_run(repo, args.run_id)
    attempt, sha = r["run_attempt"], r["head_sha"]
    base = controller / "build/apple-follow"
    base.mkdir(parents=True, exist_ok=True)
    lock = base / f"{args.run_id}-{attempt}.lock"
    try:
        lock.mkdir()
    except FileExistsError:
        raise ValueError(f"this run is already being followed: {lock}; remove a stale lock only after its process has stopped")
    try:
        session = Path(tempfile.mkdtemp(prefix=f"{args.run_id}-{attempt}-", dir=base))

        def metadata_ready():
            latest = check_run(repo, args.run_id, attempt, sha)
            item = artifact(repo, args.run_id, f"jiejiebox-apple-release-{sha}")
            if item:
                return item
            require(latest["status"] != "completed", "the run finished without frozen release metadata; use a new Apple build")
            return None

        item = wait_for("Waiting for frozen Apple release metadata", metadata_ready, args.timeout, args.interval)
        download(repo, item, session / "metadata")
        c = validate_candidate(read_json(session / "metadata/apple-release.json"))
        require(c["repository"].lower() == repo.lower() and c["run_id"] == int(args.run_id)
                and c["run_attempt"] == attempt and c["parent_sha"] == sha, "metadata belongs to a different run/attempt/commit")
        platforms = ["ios", "macos"] if args.platform == "both" else [args.platform]
        require(all(c["build_" + p] for p in platforms), "the selected run did not enable every requested platform")
        root = session / "source"
        run("git", "fetch", "origin", sha, cwd=controller)
        run("git", "worktree", "add", "--detach", str(root), sha, cwd=controller)
        require((root / "release/JIEJIE_VERSION").read_text().strip() == c["jj_version"], "candidate version differs from its commit")
        tag_matches(root, c)  # A missing tag may wait; a conflicting tag must fail now.
        # Signing settings stay on this Mac and are never included in CI metadata.
        local_env = controller / ".env.apple.local"
        if local_env.exists():
            run("git", "check-ignore", "-q", ".env.apple.local", cwd=root)
            shutil.copyfile(local_env, root / ".env.apple.local")
            (root / ".env.apple.local").chmod(0o600)
        env = os.environ.copy()
        env.update(APPLE_CLIENT_REF=c["apple_client_sha"], APPLE_CLIENT_SHA=c["apple_client_sha"],
                   APPLE_BUILD_NUMBER=os.environ.get("APPLE_BUILD_NUMBER") or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M"),
                   APPLE_CLIENT_STATE="build/apple-client-source.env", APPLE_SIGNING_MODE="testflight")
        (root / "build").mkdir(exist_ok=True)
        state = root / "build/apple-client-source.env"
        state.write_text(f"APPLE_CLIENT_SHA={c['apple_client_sha']}\nAPPLE_CLIENT_REPOSITORY={c['apple_client_repository']}\n"
                         f"APPLE_CLIENT_BRANCH={c['apple_client_branch']}\n", encoding="utf-8")
        if "ios" in platforms:
            subprocess.run(["git", "submodule", "update", "--init", "clients/apple"],
                           cwd=root, env=env, check=True)
        for platform in platforms:
            subprocess.run(["bash", "scripts/ci/apple-client-source.sh", "checkout", platform],
                           cwd=root, env=env, check=True)

        def libbox_ready():
            latest = check_run(repo, args.run_id, attempt, sha)
            producer = "libbox" if c["build_ios"] else "macos"
            ready = check_jobs(jobs_for(repo, args.run_id, attempt), [producer], latest["status"] == "completed")
            item = artifact(repo, args.run_id, f"jiejiebox-libbox-{sha}")
            require(latest["status"] != "completed" or item is not None, "the run finished without Libbox")
            # In macOS-only mode the producer and GUI share a runner. Its
            # uploaded library may be archived locally while GUI CI continues.
            return item if ready or (producer == "macos" and latest["status"] != "completed") else None

        libitem = wait_for(f"{c['release_tag']}: waiting for Libbox; source preparation can proceed independently of the Release page",
                           libbox_ready, args.timeout, args.interval)
        lib = root / "build/apple-follow-libbox"
        download(repo, libitem, lib)
        m = read_json(lib / "manifest.json")
        verify_manifest(m, c)
        require(m.get("apple_release") == c, "Libbox and early metadata disagree")
        subprocess.run(["bash", "scripts/ci/apple-libbox-artifact.sh", "validate", str(lib), "--parent-sha", sha,
                        "--jj-version", c["jj_version"]], cwd=root, env=env, check=True)
        for platform in platforms:
            directory = run("bash", "scripts/ci/apple-client-source.sh", "dir", platform, cwd=root, env=env)
            subprocess.run(["bash", "scripts/ci/apple-libbox-artifact.sh", "install", str(lib), directory], cwd=root, env=env, check=True)
        context_path = root / "build/apple-follow-context.json"
        write_json(context_path, dict(candidate=c, platforms=platforms, build_number=env["APPLE_BUILD_NUMBER"],
                                     artifact_sha256=m["artifact_sha256"], artifact_id=libitem["id"],
                                     interval=args.interval, timeout=args.timeout))
        env.update(APPLE_RELEASE_CONTEXT=str(context_path), APPLE_USE_PREBUILT_LIBBOX="1")
        print(f"Mac workspace: {root}\nTestFlight: {c['jj_version']} ({env['APPLE_BUILD_NUMBER']})\n"
              f"Formal Release must use: apple_run_id={c['run_id']} apple_run_attempt={attempt}", flush=True)
        target = "testflight" if args.platform == "both" else "testflight-" + args.platform
        subprocess.run(["bash", "scripts/release-apple.sh", target], cwd=root, env=env, check=True)
        write_json(session / "published.json", read_json(context_path))
    finally:
        lock.rmdir()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)
    c = sub.add_parser("candidate")
    for name in ("apple-sha", "apple-repository", "apple-branch", "out"):
        c.add_argument("--" + name, required=True)
    for name in ("build-ios", "build-macos"):
        c.add_argument("--" + name, choices=("true", "false"), required=True)
    c.set_defaults(func=candidate)
    b = sub.add_parser("bind")
    b.add_argument("--candidate", required=True)
    b.add_argument("--manifest", required=True)
    b.set_defaults(func=bind)
    v = sub.add_parser("verify-release")
    v.add_argument("--manifest", required=True)
    v.add_argument("--repo", required=True)
    v.add_argument("--run-id", default="")
    v.add_argument("--attempt", default="")
    v.set_defaults(func=verify_release)
    a = sub.add_parser("archive-version")
    a.add_argument("--app", required=True)
    a.add_argument("--version", required=True)
    a.add_argument("--build-number", required=True)
    a.set_defaults(func=archive_version)
    s = sub.add_parser("client-sha")
    for name in ("manifest", "sha", "version", "repo", "apple-repository"):
        s.add_argument("--" + name, required=True)
    s.set_defaults(func=client_sha)
    f = sub.add_parser("follow")
    f.add_argument("--follow-run", dest="run_id", type=int, required=True)
    f.add_argument("--repo")
    f.add_argument("--platform", choices=("ios", "macos", "both"), default="ios")
    f.add_argument("--interval", type=int, default=30)
    f.add_argument("--timeout", type=int, default=5400)
    f.set_defaults(func=follow)
    g = sub.add_parser("upload-gate")
    g.add_argument("--context", required=True)
    g.add_argument("--platform", choices=("ios", "macos"), required=True)
    g.set_defaults(func=upload_gate)
    args = p.parse_args()
    if args.command == "follow":
        if min(args.run_id, args.interval, args.timeout) <= 0:
            p.error("run ID and wait settings must be positive")
    try:
        args.func(args)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError, zipfile.BadZipFile) as e:
        print(f"apple-release: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
