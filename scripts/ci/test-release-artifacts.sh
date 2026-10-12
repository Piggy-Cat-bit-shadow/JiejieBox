#!/usr/bin/env bash
# Tests for release-artifacts.sh, and for the release workflow that calls it.
#
# Usage: test-release-artifacts.sh
# Exit: 0 = every case behaved as required, 1 = a case did not.
#
# # Why this file exists
#
# The release gate is the last thing between a commit and a published binary, and
# its failures are the expensive kind: the old workflow WAITED 90 MINUTES and then
# failed when a commit had no CI runs (run 37811184607, tag v1.15.0-alpha.7, log
# shows "runs: 0" from 16:44:51Z to 18:14:55Z), and it could not pass at all for a
# commit whose workflow had been re-run - 13 of the last 100 commits on each
# product workflow carry two or three runs, and the old condition demanded exactly
# two rows.
#
# Neither of those can be caught by a happy-path test, and both are cheap to
# reproduce with a mock `gh`. Most cases below are therefore NEGATIVE: each one
# asserts that a specific wrong release is refused. The mock never calls the
# network, so this runs in seconds rather than in 90 minutes, and it runs the REAL
# jq filters through a real `jq` where one is available.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
gate="$script_dir/release-artifacts.sh"

[ -f "$gate" ] || { echo "FAIL: gate not found at $gate" >&2; exit 1; }

work="$(mktemp -d -t jiejie-release-artifacts-XXXXXX)"
trap 'rm -rf "$work"' EXIT

failures=0
cases=0

report() { # <name> <expected 0|nonzero> <actual> <detail>
  local name="$1" expected="$2" actual="$3" detail="${4:-}"
  cases=$((cases + 1))
  local ok=0
  if [ "$expected" = "0" ]; then
    [ "$actual" -eq 0 ] && ok=1
  else
    [ "$actual" -ne 0 ] && ok=1
  fi
  if [ "$ok" = "1" ]; then
    printf 'PASS  %-56s %s\n' "$name" "$detail"
  else
    printf 'FAIL  %-56s expected exit %s, got %s %s\n' "$name" "$expected" "$actual" "$detail"
    failures=$((failures + 1))
  fi
}

# fail_case <name> <detail>
#
# A case that failed for a reason the exit code alone cannot express - a refusal
# with the WRONG message is a broken check even though it did refuse, and `report`
# judges only the exit code.
fail_case() {
  cases=$((cases + 1))
  failures=$((failures + 1))
  printf 'FAIL  %-56s %s\n' "$1" "$2"
}

SHA="1111111111111111111111111111111111111111"
LINUX_WF="server-linux-amd64.yml"
MACOS_WF="client-macos.yml"
LINUX_ID=101
MACOS_ID=202

# ---------------------------------------------------------------------------
# The mock gh
# ---------------------------------------------------------------------------
#
# It speaks the same contract the gate uses - `gh api <path> --jq <filter>`,
# `gh run download <id> --dir <dir>` - and it applies the jq filter with the real
# `jq`, so a broken filter in the gate fails here rather than in production. It
# records every call, which is how "dry_run never published" is proven: the
# publish step is a real `gh release create`, and the test asserts it never ran.

mkdir -p "$work/bin"

cat > "$work/bin/gh" <<'MOCK_GH'
#!/usr/bin/env python3
"""Mock `gh` for the release-artifacts tests. State lives in $MOCK_STATE."""
import json
import os
import pathlib
import shutil
import subprocess
import sys

state = pathlib.Path(os.environ["MOCK_STATE"])
scenario = json.loads((state / "scenario.json").read_text())
fixtures = pathlib.Path(os.environ["MOCK_FIXTURES"])


def record(line):
    with (state / "calls.log").open("a") as fh:
        fh.write(line + "\n")


def bump(key):
    path = state / ("count-" + key)
    n = int(path.read_text()) if path.exists() else 0
    path.write_text(str(n + 1))
    return n


def fail(msg, code=1):
    print("mock-gh: " + msg, file=sys.stderr)
    sys.exit(code)


def jq_filter(document, filt):
    """Applies the gate's own --jq filter. Real jq when available."""
    exe = shutil.which("jq")
    if exe:
        done = subprocess.run([exe, "-r", filt], input=json.dumps(document),
                              text=True, capture_output=True)
        if done.returncode != 0:
            fail("jq rejected the filter %r: %s" % (filt, done.stderr.strip()))
        return done.stdout
    # Fallback for a runner without jq: the two filters the gate uses, translated.
    if filt == "[.id, .path, .state] | @tsv":
        return "%s\t%s\t%s\n" % (document["id"], document["path"], document["state"])
    if filt.startswith(".workflow_runs[]"):
        # Mirrors the gate's filter, including the normalisation of a null or empty
        # conclusion to "-": an empty TSV field is consumed by bash `read`.
        out = []
        for run in document["workflow_runs"]:
            row = []
            for key in ("id", "run_number", "status", "conclusion", "created_at",
                        "html_url", "head_sha", "workflow_id"):
                value = run.get(key)
                if key == "conclusion" and not value:
                    value = "-"
                row.append("" if value is None else str(value))
            out.append("\t".join(row))
        return "".join(line + "\n" for line in out)
        return "".join(line + "\n" for line in out)
    fail("no jq on this machine and no translation for filter %r" % filt)


def workflows():
    return scenario["workflows"]


def find_workflow(key):
    """Resolve by file name or numeric id, as the API does."""
    for name, wf in workflows().items():
        if key == name or key == str(wf["id"]):
            return name, wf
    return None, None


def materialise_run(name, wf, run):
    """Applies a status sequence, so a run can advance between polls."""
    steps = run.get("steps")
    if not steps:
        return run
    seen = bump("polls-" + name)
    step = steps[min(seen, len(steps) - 1)]
    merged = dict(run)
    merged.update(step)
    return merged


def api(args):
    path = None
    filt = None
    i = 0
    while i < len(args):
        if args[i] == "--jq":
            filt = args[i + 1]
            i += 2
            continue
        if args[i] in ("--paginate", "-X", "GET"):
            i += 1
            continue
        path = args[i]
        i += 1
    if path is None:
        fail("api: no path given")
    record("api %s" % path)

    parts = path.split("/")
    # repos/<owner>/<repo>/actions/workflows/<key>[/runs?...]
    if len(parts) < 6 or parts[3] != "actions" or parts[4] != "workflows":
        fail("api: unsupported path %r" % path)
    key = parts[5]
    query = ""
    if "?" in key:
        key, query = key.split("?", 1)

    if len(parts) == 6:
        name, wf = find_workflow(key)
        if wf is None:
            fail("api: workflow %r not found" % key, 1)
        document = {"id": wf["id"], "path": wf["path"], "state": wf["state"],
                    "name": wf.get("name", name)}
        sys.stdout.write(jq_filter(document, filt) if filt else json.dumps(document))
        return 0

    if len(parts) == 7 and parts[6].startswith("runs"):
        # The query string belongs to the /runs segment, not to the workflow key:
        # reading it off parts[5] silently drops the head_sha filter and returns
        # every run of the workflow.
        if "?" in parts[6]:
            query = parts[6].split("?", 1)[1]
        name, wf = find_workflow(key)
        if wf is None:
            fail("api: workflow %r not found" % key, 1)
        wanted = None
        for pair in query.split("&"):
            if pair.startswith("head_sha="):
                wanted = pair.split("=", 1)[1]
        runs = []
        for run in wf["runs"]:
            run = materialise_run(name, wf, run)
            if wanted is None or run["head_sha"] == wanted:
                runs.append(run)
        document = {"total_count": len(runs), "workflow_runs": runs}
        sys.stdout.write(jq_filter(document, filt) if filt else json.dumps(document))
        return 0

    fail("api: unsupported path %r" % path)


def run_download(args):
    run_id = args[0]
    rid = str(run_id)
    directory = None
    i = 1
    while i < len(args):
        if args[i] == "--dir":
            directory = args[i + 1]
            i += 2
            continue
        if args[i] == "--repo":
            i += 2
            continue
        fail("run download: unexpected argument %r" % args[i])
    if directory is None:
        fail("run download: --dir is required")
    record("run download %s" % rid)

    entry = scenario.get("artifacts", {}).get(rid)
    if entry is None:
        fail("no artifacts found for run %s" % rid)
    if entry.get("download_fails"):
        fail("HTTP 500: could not download artifact for run %s" % rid)

    dest = pathlib.Path(directory)
    if entry.get("no_artifacts"):
        # The run exists but uploaded nothing: the directory is created empty.
        dest.mkdir(parents=True, exist_ok=True)
        return 0

    source = fixtures / entry["dir"]
    target = dest / entry["name"]
    if target.exists():
        shutil.rmtree(target)
    shutil.copytree(source, target)
    if entry.get("duplicate_artifact"):
        # A second artifact directory holding another copy of the binary. The
        # release must refuse this rather than pick one.
        shutil.copytree(source, dest / (entry["name"] + "-again"))
    return 0


def main():
    args = sys.argv[1:]
    if not args:
        fail("no arguments")
    if args[0] == "api":
        return api(args[1:])
    if args[0] == "run" and len(args) > 1 and args[1] == "download":
        return run_download(args[2:])
    if args[0] == "run" and len(args) > 1 and args[1] == "list":
        # The old implementation's query. If it comes back, the 60-run window and
        # the count==2 assumption come back with it.
        record("UNEXPECTED run list")
        fail("run list must not be used to select release artifacts")
    if args[0] == "release" and len(args) > 1 and args[1] == "create":
        record("release create " + " ".join(args[2:]))
        (state / "release_created").write_text(" ".join(args[2:]))
        return 0
    if args[0] == "release" and len(args) > 1 and args[1] == "view":
        # The publish step reads the release back and asserts the published asset set is
        # exactly the public contract, so the mock answers that query rather than printing
        # a canned summary.
        record("release view " + " ".join(args[2:]))
        tag = args[2] if len(args) > 2 else "v0.1"
        assets = scenario.get("published_assets")
        if assets is None:
            # Default: what a correct release published for this tag.
            version = scenario.get("version", "0.1")
            assets = [
                "Jiejiebox-v%s-windows-x64.exe" % version,
                "jiejie-sing-box-android-arm64-%s.tar.gz" % tag,
                "jiejie-sing-box-linux-amd64-%s.tar.gz" % tag,
                "jiejie-sing-box-macos-arm64-%s.tar.gz" % tag,
                "jiejie-sing-box-windows-amd64-%s.tar.gz" % tag,
                "SHA256SUMS",
            ]
        if "--json" in args:
            want = args[args.index("--json") + 1]
            if want == "assets":
                filt = args[args.index("--jq") + 1] if "--jq" in args else None
                document = {"assets": [{"name": n, "size": 1} for n in assets]}
                if filt == ".assets[].name":
                    sys.stdout.write("".join(n + "\n" for n in assets))
                elif filt:
                    sys.stdout.write(jq_filter(document, filt))
                else:
                    sys.stdout.write(json.dumps(document))
                return 0
            if want == "url,isDraft,isPrerelease,tagName,assets":
                print("url: https://example.invalid/releases/tag/%s" % tag)
                print("isDraft: false")
                print("isPrerelease: false")
                print("tag: %s" % tag)
                return 0
        print("url: https://example.invalid/releases/tag/%s" % tag)
        print("isDraft: false")
        print("isPrerelease: false")
        return 0
    record("UNEXPECTED " + " ".join(args))
    fail("unsupported gh invocation: %s" % " ".join(args))


sys.exit(main())
MOCK_GH
chmod +x "$work/bin/gh"

# ---------------------------------------------------------------------------
# Fixture builders
# ---------------------------------------------------------------------------

python3 - "$work" <<'PY'
import pathlib, sys
work = pathlib.Path(sys.argv[1])
(work / "fixtures").mkdir(exist_ok=True)
PY

# make_binary <path> <kind> - a file with the platform magic the gate checks.
#
# The two-product cases only ever needed ELF and Mach-O. The four-product contract case
# needs every kind the gate can verify, so `pe-amd64` and `aar-arm64` are built here in the
# same shape the gate reads:
#
#   pe-amd64   'MZ', the PE header offset at 0x3c, 'PE\0\0' at that offset and machine
#              0x8664. The gate reads exactly these four things.
#   aar-arm64  a real ZIP holding jni/arm64-v8a/libbox.so as a little-endian AArch64 ELF,
#              which is what the gate unzips and inspects.
make_binary() {
  python3 - "$1" "$2" <<'PY'
import pathlib, struct, sys, zipfile, io
path = pathlib.Path(sys.argv[1]); kind = sys.argv[2]

def elf_arm64():
    buf = bytearray(4096)
    buf[0:4] = b"\x7fELF"; buf[4] = 2; buf[5] = 1; buf[6] = 1
    buf[16:18] = (2).to_bytes(2, "little")        # ET_EXEC
    buf[18:20] = (0xB7).to_bytes(2, "little")     # EM_AARCH64
    return bytes(buf)

if kind == "pe-amd64":
    buf = bytearray(4096)
    buf[0:2] = b"MZ"
    # e_lfanew: the gate seeks to this offset and expects the PE signature there.
    struct.pack_into("<I", buf, 0x3C, 0x80)
    buf[0x80:0x84] = b"PE\0\0"
    struct.pack_into("<H", buf, 0x84, 0x8664)     # IMAGE_FILE_MACHINE_AMD64
    path.write_bytes(bytes(buf))
elif kind == "aar-arm64":
    # The gate calls `unzip -p <file> jni/arm64-v8a/libbox.so` and reads the ELF magic from
    # the result, so the entry name and the AArch64 machine field both have to be real.
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("jni/arm64-v8a/libbox.so", elf_arm64())
else:
    buf = bytearray(4096)
    if kind == "elf-x86-64":
        buf[0:4] = b"\x7fELF"; buf[4] = 2; buf[5] = 1; buf[6] = 1
        buf[16:18] = (2).to_bytes(2, "little")        # ET_EXEC
        buf[18:20] = (0x3E).to_bytes(2, "little")     # EM_X86_64
    elif kind == "macho-arm64":
        buf[0:4] = b"\xcf\xfa\xed\xfe"
        buf[4:8] = (0x0100000C).to_bytes(4, "little")  # CPU_TYPE_ARM64
        buf[12:16] = (2).to_bytes(4, "little")         # MH_EXECUTE
    elif kind == "elf-arm64":
        buf[0:4] = b"\x7fELF"; buf[4] = 2; buf[5] = 1; buf[6] = 1
        buf[16:18] = (2).to_bytes(2, "little")
        buf[18:20] = (0xB7).to_bytes(2, "little")     # EM_AARCH64
    else:
        raise SystemExit("unknown kind " + kind)
    path.write_bytes(bytes(buf))
PY
}

sha_of() { # portable, matching scripts/ci/lib.sh
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# The desktop control fixture every verify case is run against. Built here because it needs
# make_binary and sha_of above.
DESKTOP_DIR="$work/desktop-control"

# make_desktop <dir> <version> <core sha> [flags...]
#
# The Windows desktop artifact as its workflow uploads it: the signed-test installer plus a
# build record. `flags` injects the defects the release must refuse:
#
#   no-record        the build record is absent (nothing proves the installer's core)
#   record-sha       the record names another core commit
#   record-version   the record names another version
#   bytes            the record's byte count does not match the file
#   hash             the record's sha256 does not match the file
#   wrong-name       the installer is named for another version
#   extra-record     a CI record that must not be published
#
# The record's shape mirrors the real one: labels padded, values after runs of spaces,
# read by `sed -n 's/^<label>[[:space:]]\{1,\}//p'`.
make_desktop() {
  local dir="$1" version="$2" core_sha="$3"
  shift 3
  local flags=" $* "
  rm -rf "$dir"; mkdir -p "$dir"

  local name="Jiejiebox-v${version}-windows-x64.exe"
  [[ "$flags" == *" wrong-name "* ]] && name="Jiejiebox-v9.9.9-windows-x64.exe"
  make_binary "$dir/$name" "elf-x86-64"

  local bytes hash record_sha="$core_sha" record_version="$version"
  bytes="$(wc -c < "$dir/$name" | tr -d ' ')"
  hash="$(sha_of "$dir/$name")"
  [[ "$flags" == *" record-sha "* ]] && record_sha="2222222222222222222222222222222222222222"
  [[ "$flags" == *" record-version "* ]] && record_version="9.9.9"
  [[ "$flags" == *" bytes "* ]] && bytes="$((bytes + 1))"
  [[ "$flags" == *" hash "* ]] && hash="3333333333333333333333333333333333333333333333333333333333333333"

  if [[ "$flags" != *" no-record "* ]]; then
    {
      printf 'Jiejiebox Windows installer build record\n\n'
      printf 'core SHA          %s\n' "$record_sha"
      printf 'core version      %s\n' "$record_version"
      printf 'artifact          %s\n' "$name"
      printf 'artifact bytes    %s\n' "$bytes"
      printf 'artifact sha256   %s\n' "$hash"
    } > "$dir/BUILD-INFO-WINDOWS-DESKTOP-${version}.txt"
  fi

  if [[ "$flags" == *" extra-record "* ]]; then
    printf 'runner: ubuntu-latest\n' > "$dir/runner-environment.txt"
  fi
}

# Built once, before any verify case runs.
make_desktop "$DESKTOP_DIR" "0.1" "$SHA"

# make_artifact <dir> <binary> <buildinfo> <platform> <arch> <kind> <commit> [flags...]
#
# flags: no-sha256 | bad-sha256 | bad-commit | bad-artifact-hash | extra-file
make_artifact() {
  local dir="$1" binary="$2" buildinfo="$3" platform="$4" arch="$5" kind="$6" commit="$7"
  shift 7
  local flags=" $* "
  rm -rf "$dir"; mkdir -p "$dir"
  make_binary "$dir/$binary" "$kind"
  printf 'placeholder licence\n' > "$dir/LICENSE"

  local hash
  hash="$(sha_of "$dir/$binary")"
  case "$flags" in *" bad-sha256 "*) hash="0000000000000000000000000000000000000000000000000000000000000000" ;; esac

  local recorded_commit="$commit"
  case "$flags" in *" bad-commit "*) recorded_commit="2222222222222222222222222222222222222222" ;; esac

  local recorded_hash="$hash"
  case "$flags" in
    *" bad-artifact-hash "*) recorded_hash="3333333333333333333333333333333333333333333333333333333333333333" ;;
    # The BUILD-INFO hash is computed over the REAL bytes, so a deliberately
    # corrupted .sha256 leaves the sidecar disagreeing with the file - which is
    # what the gate must catch when it compares the two.
  esac

  {
    echo "Jiejie sing-box build information"
    echo "================================="
    echo "flavor:                server"
    echo "platform:              $platform"
    echo "arch:                  $arch"
    echo "version:               0.1"
    echo "git_commit:            $recorded_commit"
    echo "build_time_utc:        2026-10-08T00:00:00Z"
    echo ""
    echo "artifact:              $binary"
    echo "artifact_bytes:        $(wc -c < "$dir/$binary" | tr -d ' ')"
    echo "artifact_sha256:       $recorded_hash"
  } > "$dir/$buildinfo"

  case "$flags" in
    *" no-sha256 "*) ;;
    *) printf '%s  %s\n' "$hash" "$binary" > "$dir/$binary.sha256" ;;
  esac
  case "$flags" in *" extra-file "*) printf 'stray\n' > "$dir/UNEXPECTED.txt" ;; esac
}

# ---------------------------------------------------------------------------
# Scenario builder
# ---------------------------------------------------------------------------
#
# Runs are emitted in a deliberately shuffled order and with a large pile of
# unrelated runs around them: the old implementation read a 60-row window over
# the whole repository and would have seen the wrong thing entirely.

# write_scenario <name> <python that mutates `scenario`>
write_scenario() {
  MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
    python3 - "$1" "$SHA" "$LINUX_ID" "$MACOS_ID" "$LINUX_WF" "$MACOS_WF" <<PY
import json, os, pathlib, sys
name, sha, linux_id, macos_id, linux_wf, macos_wf = sys.argv[1:7]
state = pathlib.Path(os.environ["MOCK_STATE"])
state.mkdir(parents=True, exist_ok=True)
for stale in state.glob("count-*"):
    stale.unlink()
for stale in ("release_created", "calls.log"):
    p = state / stale
    if p.exists():
        p.unlink()

def run(rid, number, status="completed", conclusion="success", workflow_id=linux_id):
    return {"id": rid, "run_number": number, "status": status, "conclusion": conclusion,
            "created_at": "2026-10-08T10:%02d:00Z" % (number % 60),
            "html_url": "https://example.invalid/runs/%d" % rid,
            "head_sha": sha, "workflow_id": workflow_id}

def wf(wid, path, state_value="active", runs=None):
    return {"id": wid, "path": path, "state": state_value, "name": path,
            "runs": runs if runs is not None else []}

# Unrelated activity: 70 runs of each product workflow for OTHER commits, plus a
# third workflow. None of it may influence the selection.
noise = []
for i in range(70):
    other = run(50000 + i, 100 + i)
    other["head_sha"] = "%040d" % (i + 1)
    noise.append(other)

scenario = {
    "workflows": {
        linux_wf: wf(linux_id, ".github/workflows/" + linux_wf,
                     runs=[run(9001, 10, workflow_id=linux_id)] + noise + [run(9002, 11, workflow_id=linux_id)]),
        macos_wf: wf(macos_id, ".github/workflows/" + macos_wf,
                     runs=[run(9101, 20, workflow_id=macos_id)] + noise[::-1] + [run(9102, 21, workflow_id=macos_id)]),
        "unrelated.yml": wf(303, ".github/workflows/unrelated.yml",
                            runs=[run(9900, 1, workflow_id=303)]),
    },
    "artifacts": {},
}
scenario["artifacts"]["9002"] = {"dir": "linux", "name": "Jiejie-Linux-amd64-0.1-1111111"}
scenario["artifacts"]["9102"] = {"dir": "macos", "name": "Jiejie-macOS-arm64-0.1-1111111"}

# --- case mutation ---------------------------------------------------------
$2

(state / "scenario.json").write_text(json.dumps(scenario, indent=1))
PY
}

# run_gate <args...> — runs the gate against the mock.
run_gate() {
  PATH="$work/bin:$PATH" MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
    "$gate" "$@"
}

# verify_with_desktop <args...>
#
# `verify` takes the desktop installer as a required input, because a release without it
# would still pass every other check. Every verify case here goes through this wrapper so
# the requirement is satisfied in one place rather than repeated at a dozen call sites,
# and so a case that fails does so for the reason it is testing rather than for the
# missing installer. DESKTOP_DIR is created below, once make_desktop exists.
verify_with_desktop() {
  run_gate verify --desktop-dir "$DESKTOP_DIR" --version 0.1 "$@"
}

# gate_setup <label> <gate args...>
#
# A prerequisite command for a later assertion. Its failure is RECORDED as a failed
# case and the suite continues, rather than tripping `set -e` and truncating the
# run: a truncated run looks exactly like a short green one, which is how an
# earlier version of this file silently skipped its last three cases.
gate_setup() {
  local label="$1"; shift
  local out rc=0
  out="$(run_gate "$@" 2>&1)" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fail_case "$label" "prerequisite failed: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-140)"
  fi
  return 0
}

# expect_ok <name> <args...>  or  expect_fail <name> <substring> -- <args...>
expect_ok() {
  local name="$1"; shift
  local out rc=0
  out="$(run_gate "$@" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    report "$name" 0 "$rc" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-70)"
  else
    # A failing case must show WHY. The gate's own message is the whole diagnosis, and a
    # 70-character summary of it is a puzzle rather than a report - which is exactly what
    # happened the first time this suite ran on a runner after `verify` gained a required
    # argument. Only the passing path is summarised.
    report "$name" 0 "$rc" ""
    printf '      %s\n' "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-600)"
  fi
}

expect_fail() {
  local name="$1" want="$2"; shift 2
  if [ "${1:-}" = "--" ]; then shift; fi
  local out rc=0
  out="$(run_gate "$@" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    fail_case "$name" "it SUCCEEDED; the command must have failed"
    return
  fi
  if printf '%s' "$out" | grep -qF -- "$want"; then
    report "$name" nonzero "$rc" "refused: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-56)"
  else
    fail_case "$name" "refused for the WRONG reason; wanted message containing '$want'"
    printf '      actual output: %s\n' "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
  fi
}

# ---------------------------------------------------------------------------
# Fixtures on disk
# ---------------------------------------------------------------------------

make_artifact "$work/fixtures/linux" sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA"
make_artifact "$work/fixtures/macos" sing-box-darwin-arm64 BUILD-INFO-MACOS.txt darwin arm64 macho-arm64 "$SHA"

# ---------------------------------------------------------------------------
# Case 1: both platforms green -> PASS, with the selected run IDs recorded
# ---------------------------------------------------------------------------

write_scenario base "pass"
plan="$work/plan.tsv"
expect_ok "both platforms green -> PASS" plan --repo o/r --sha "$SHA" --out "$plan" --interval 1 --timeout 10
if [ -f "$plan" ]; then
  got="$(cut -f3 "$plan" | sort | tr '\n' ',')"
  if [ "$got" = "9002,9102," ]; then
    report "plan records the newest successful run per workflow" 0 0 "run ids $got"
  else
    report "plan records the newest successful run per workflow" 0 1 "got '$got', want '9002,9102,'"
  fi
  if [ "$(cut -f7 "$plan" | sort -u)" = "$SHA" ]; then
    report "plan records the verified commit" 0 0 "$SHA"
  else
    report "plan records the verified commit" 0 1 "wrong sha in plan"
  fi
fi

expect_ok "download from the planned runs -> PASS" download --repo o/r --plan "$plan" --dir "$work/assets"

# The desktop installer is staged from its own workflow artifact and verified from its
# build record, so it is a required input to both verify and flatten.
make_desktop "$work/desktop" "0.1" "$SHA"
expect_ok "verify both platforms and the desktop installer -> PASS" \
  verify --desktop-dir "$work/desktop" --version 0.1 --dir "$work/assets" --sha "$SHA"
expect_ok "flatten -> PASS" flatten --plan "$plan" --dir "$work/assets" --out "$work/upload" \
  --tag v0.1 --desktop "$work/desktop/Jiejiebox-v0.1-windows-x64.exe" --version 0.1

# The published set is the contract, and its size is the size of THIS suite's product
# table - the two workflows the fixtures build, not the four a release ships. The counts are
# computed from that number with the two non-product assets added, rather than written as
# literals: an earlier version of this case asserted six files and five entries, which is the
# four-product release, and it read as "wrong asset set" without saying which side was wrong.
PRODUCTS=2
want_files=$(( PRODUCTS + 2 ))          # products + installer + SHA256SUMS
want_entries=$(( PRODUCTS + 1 ))       # products + installer
if [ "$(find "$work/upload" -maxdepth 1 -type f | wc -l | tr -d ' ')" = "$want_files" ] \
   && [ -f "$work/upload/jiejie-sing-box-linux-amd64-v0.1.tar.gz" ] \
   && [ -f "$work/upload/jiejie-sing-box-macos-arm64-v0.1.tar.gz" ] \
   && [ -f "$work/upload/Jiejiebox-v0.1-windows-x64.exe" ] \
   && [ -f "$work/upload/SHA256SUMS" ] \
   && [ "$(grep -c . "$work/upload/SHA256SUMS")" = "$want_entries" ]; then
  report "flatten emits the public binaries and one manifest" 0 0 "$want_files files, $want_entries entries"
else
  report "flatten emits the public binaries and one manifest" 0 1 \
    "want $want_files files / $want_entries entries, got $(find "$work/upload" -maxdepth 1 -type f | wc -l | tr -d ' ') files / $(grep -c . "$work/upload/SHA256SUMS" 2>/dev/null || echo none) entries"
fi

# The manifest must name the files the way the download page does - bare names, no './' -
# or a downloader's `sha256sum -c` would look for a path that does not exist.
if [ "$(sed -n 's/^[0-9a-f]\{64\}  //p' "$work/upload/SHA256SUMS" | grep -c '^\./')" = "0" ] \
   && [ "$(sed -n 's/^[0-9a-f]\{64\}  //p' "$work/upload/SHA256SUMS" | grep -c 'Jiejiebox-')" = "1" ]; then
  report "the manifest names bare filenames and covers the installer" 0 0
else
  report "the manifest names bare filenames and covers the installer" 0 1 "manifest shape wrong"
fi

# And it must actually verify, in the directory it describes.
if ( cd "$work/upload" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ); then
  report "sha256sum -c SHA256SUMS passes in the upload directory" 0 0
else
  report "sha256sum -c SHA256SUMS passes in the upload directory" 0 1 "manifest does not verify"
fi

expect_ok "check-upload accepts the staged contract" \
  check-upload --out "$work/upload" --tag v0.1 --version 0.1

# The tarball contents are pinned, so the shipped file set cannot grow silently.
# The `./` entry is tar's own directory record, not a file in the artifact.
tar -tzf "$work/upload/jiejie-sing-box-linux-amd64-v0.1.tar.gz" | grep -vx './' | sed 's|^\./||' | sort > "$work/tar-list.txt"
printf '%s\n' BUILD-INFO-LINUX.txt LICENSE sing-box-linux-amd64 sing-box-linux-amd64.sha256 > "$work/tar-want.txt"
if diff -q "$work/tar-list.txt" "$work/tar-want.txt" >/dev/null; then
  report "linux tarball holds exactly the verified files" 0 0
else
  report "linux tarball holds exactly the verified files" 0 1 "$(tr '\n' ' ' < "$work/tar-list.txt")"
fi

# ---------------------------------------------------------------------------
# Case 2: the 60-run window and the count==2 assumption
# ---------------------------------------------------------------------------
#
# The base scenario already carries 70 unrelated runs per workflow and a stale
# earlier run for the same commit, in shuffled order. Case 1 passing proves the
# selection is immune to both. The cases below pin the specific old failure modes.

write_scenario reruns \
  'scenario["workflows"][linux_wf]["runs"] += [run(9003, 12, workflow_id=linux_id), run(9004, 13, workflow_id=linux_id)]'
grep_plan="$work/plan-reruns.tsv"
expect_ok "three runs for the same commit -> still PASS" plan --repo o/r --sha "$SHA" --out "$grep_plan" --interval 1 --timeout 10
if [ "$(cut -f3 "$grep_plan" | sort | tr '\n' ',')" = "9004,9102," ]; then
  report "the newest run of a re-run workflow is selected" 0 0 "9004"
else
  report "the newest run of a re-run workflow is selected" 0 1 "$(cut -f3 "$grep_plan" | tr '\n' ',')"
fi

write_scenario shuffled \
  'scenario["workflows"][linux_wf]["runs"] = [run(9004, 13, workflow_id=linux_id), run(9002, 11, workflow_id=linux_id), run(9003, 12, workflow_id=linux_id)]
scenario["artifacts"]["9004"] = dict(scenario["artifacts"]["9002"])'
expect_ok "runs returned newest-first -> still PASS" plan --repo o/r --sha "$SHA" --out "$work/plan-shuf.tsv" --interval 1 --timeout 10
if [ "$(cut -f3 "$work/plan-shuf.tsv" | sort | tr '\n' ',')" = "9004,9102," ]; then
  report "selection sorts by run_number instead of taking the last row" 0 0 "9004"
else
  report "selection sorts by run_number instead of taking the last row" 0 1 "$(cut -f3 "$work/plan-shuf.tsv" | tr '\n' ',')"
fi

write_scenario stale_green \
  'scenario["workflows"][macos_wf]["runs"][-1] = run(9103, 22, conclusion="failure", workflow_id=macos_id)'
expect_fail "latest macOS run failed, older green -> FAIL" "did not succeed" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-stale.tsv" --interval 1 --timeout 5

write_scenario cancelled \
  'scenario["workflows"][macos_wf]["runs"][-1] = run(9104, 23, conclusion="cancelled", workflow_id=macos_id)'
expect_fail "latest macOS run cancelled -> FAIL" "did not succeed" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-cancel.tsv" --interval 1 --timeout 5

write_scenario skipped \
  'scenario["workflows"][macos_wf]["runs"][-1] = run(9105, 24, conclusion="skipped", workflow_id=macos_id)'
expect_fail "skipped workflow -> FAIL (skip is not a pass)" "SKIPPED" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-skip.tsv" --interval 1 --timeout 5

# ---------------------------------------------------------------------------
# Case 3: no target CI at all must fail FAST with an instruction
# ---------------------------------------------------------------------------

write_scenario no_linux \
  'scenario["workflows"][linux_wf]["runs"] = [r for r in scenario["workflows"][linux_wf]["runs"] if r["head_sha"] != sha]'
start="$(date +%s)"
expect_fail "no Linux run for the commit -> FAIL" "no run of .github/workflows/server-linux-amd64.yml exists" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-nolinux.tsv" --interval 1 --timeout 5400
elapsed=$(( $(date +%s) - start ))
if [ "$elapsed" -lt 30 ]; then
  report "missing Linux CI fails immediately, not after the timeout" 0 0 "${elapsed}s (the old workflow took 5400s)"
else
  report "missing Linux CI fails immediately, not after the timeout" 0 1 "took ${elapsed}s"
fi

out="$(PATH="$work/bin:$PATH" MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
  "$gate" plan --repo o/r --sha "$SHA" --out "$work/plan-msg.tsv" --timeout 10 2>&1 || true)"
if printf '%s' "$out" | grep -q "gh workflow run server-linux-amd64.yml" \
   && printf '%s' "$out" | grep -q "gh workflow run client-macos.yml"; then
  report "the no-CI failure names both dispatch commands" 0 0
else
  report "the no-CI failure names both dispatch commands" 0 1 "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-90)"
fi

write_scenario no_macos \
  'scenario["workflows"][macos_wf]["runs"] = [r for r in scenario["workflows"][macos_wf]["runs"] if r["head_sha"] != sha]'
expect_fail "no macOS run for the commit -> FAIL" "no run of .github/workflows/client-macos.yml exists" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-nomacos.tsv" --interval 1 --timeout 5400

# ---------------------------------------------------------------------------
# Case 4: an in-flight run is waited for, within a bound
# ---------------------------------------------------------------------------

write_scenario inflight \
  'scenario["workflows"][linux_wf]["runs"][-1] = run(9005, 14, status="in_progress", conclusion=None, workflow_id=linux_id)
scenario["workflows"][linux_wf]["runs"][-1]["steps"] = [{"status": "in_progress", "conclusion": None}, {"status": "in_progress", "conclusion": None}, {"status": "completed", "conclusion": "success"}]
scenario["artifacts"]["9005"] = scenario["artifacts"].pop("9002")'
expect_ok "in-flight run completes during the bounded wait -> PASS" \
  plan --repo o/r --sha "$SHA" --out "$work/plan-inflight.tsv" --interval 1 --timeout 30
if [ "$(cut -f3 "$work/plan-inflight.tsv" | sort | tr '\n' ',')" = "9005,9102," ]; then
  report "the run that finished during the wait is the one selected" 0 0 "9005"
else
  report "the run that finished during the wait is the one selected" 0 1 "$(cut -f3 "$work/plan-inflight.tsv" | tr '\n' ',')"
fi

write_scenario forever \
  'scenario["workflows"][linux_wf]["runs"][-1] = run(9006, 15, status="in_progress", conclusion=None, workflow_id=linux_id)'
start="$(date +%s)"
expect_fail "a run that never finishes -> bounded timeout FAIL" "timed out after 3s" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-forever.tsv" --interval 1 --timeout 3
elapsed=$(( $(date +%s) - start ))
if [ "$elapsed" -lt 30 ]; then
  report "the timeout is honoured (3s, not 5400s)" 0 0 "${elapsed}s"
else
  report "the timeout is honoured (3s, not 5400s)" 0 1 "took ${elapsed}s"
fi

# ---------------------------------------------------------------------------
# Case 5: the download step must not swallow failures
# ---------------------------------------------------------------------------

write_scenario dl_fail 'scenario["artifacts"]["9002"]["download_fails"] = True'
gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/plan-dl.tsv" --interval 1 --timeout 10
expect_fail "a failing gh run download -> FAIL" "gh run download failed" -- \
  download --repo o/r --plan "$work/plan-dl.tsv" --dir "$work/assets-dl"

write_scenario no_artifacts 'scenario["artifacts"]["9002"] = {"no_artifacts": True}'
gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/plan-na.tsv" --interval 1 --timeout 10
expect_fail "a run that uploaded nothing -> FAIL" "artifact directories" -- \
  download --repo o/r --plan "$work/plan-na.tsv" --dir "$work/assets-na"

# ---------------------------------------------------------------------------
# Case 6: per-platform integrity, every failure mode
# ---------------------------------------------------------------------------

write_scenario dup 'scenario["artifacts"]["9002"]["duplicate_artifact"] = True'
gate_setup "plan for the two-artifact-directory scenario" plan --repo o/r --sha "$SHA" --out "$work/plan-dup.tsv" --interval 1 --timeout 10
expect_fail "a run publishing two artifact directories -> FAIL" "artifact directories" -- \
  download --repo o/r --plan "$work/plan-dup.tsv" --dir "$work/assets-dup"

# The download path refuses a second artifact directory, so the verify-side
# uniqueness rule is exercised on its own: two copies of the same binary inside the
# verify tree must be refused rather than one being picked.
write_scenario base_dup2 "pass"
gate_setup "plan for the duplicated-binary scenario" plan --repo o/r --sha "$SHA" --out "$work/plan-dup2.tsv" --interval 1 --timeout 10
gate_setup "download for the duplicated-binary scenario" download --repo o/r --plan "$work/plan-dup2.tsv" --dir "$work/assets-dup2"
cp -R "$work/assets-dup2/sing-box-linux-amd64/Jiejie-Linux-amd64-0.1-1111111" "$work/assets-dup2/sing-box-linux-amd64/COPY"
expect_fail "two copies of one binary in the verify tree -> FAIL" "expected exactly 1" -- \
  verify --desktop-dir "$DESKTOP_DIR" --version 0.1 --dir "$work/assets-dup2" --sha "$SHA"

# A good baseline for the negative verify cases: one broken platform, one fine.
# macOS is left valid on purpose - a Linux-only problem must still fail.
verify_case() { # <name> <expected substring> <make-linux-args...>
  local name="$1" want="$2"; shift 2
  make_artifact "$work/fixtures/linux" "$@"
  write_scenario base_v "pass"
  gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/vp.tsv" --interval 1 --timeout 10
  rm -rf "$work/vassets"
  gate_setup "download (setup)" download --repo o/r --plan "$work/vp.tsv" --dir "$work/vassets"
  expect_fail "$name" "$want" -- verify --desktop-dir "$DESKTOP_DIR" --version 0.1 --dir "$work/vassets" --sha "$SHA"
}

verify_case "missing .sha256 for one platform -> FAIL" "sing-box-linux-amd64.sha256: found 0 copies" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA" no-sha256
verify_case "wrong .sha256 for one platform -> FAIL" "does not match" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA" bad-sha256
verify_case "BUILD-INFO from another commit -> FAIL" "built from a different commit" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA" bad-commit
verify_case "BUILD-INFO artifact hash disagrees -> FAIL" "records artifact_sha256" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA" bad-artifact-hash
verify_case "ELF for the wrong architecture in the Linux slot -> FAIL" "is ELF but not x86-64" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-arm64 "$SHA"
verify_case "unexpected extra file -> FAIL" "unexpected file" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA" extra-file

# Restore the good Linux fixture.
make_artifact "$work/fixtures/linux" sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA"
write_scenario base_r "pass"
gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/plan-r.tsv" --interval 1 --timeout 10
rm -rf "$work/assets-r"
gate_setup "download (setup)" download --repo o/r --plan "$work/plan-r.tsv" --dir "$work/assets-r"
expect_ok "the restored good pair verifies again (control)" verify --desktop-dir "$DESKTOP_DIR" --version 0.1 --dir "$work/assets-r" --sha "$SHA"

# The macOS slot is enforced independently of the Linux one: a Linux binary
# renamed to the macOS binary name must not pass on the strength of its name, and
# a Linux-only failure must not be able to hide behind a green macOS artifact.
verify_case_macos() { # <name> <expected substring> <make-macos-args...>
  local name="$1" want="$2"; shift 2
  make_artifact "$work/fixtures/macos" "$@"
  write_scenario base_m "pass"
  gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/vpm.tsv" --interval 1 --timeout 10
  rm -rf "$work/vmassets"
  gate_setup "download (setup)" download --repo o/r --plan "$work/vpm.tsv" --dir "$work/vmassets"
  expect_fail "$name" "$want" -- verify --desktop-dir "$DESKTOP_DIR" --version 0.1 --dir "$work/vmassets" --sha "$SHA"
  # Restore the good macOS fixture for the cases that follow.
  make_artifact "$work/fixtures/macos" sing-box-darwin-arm64 BUILD-INFO-MACOS.txt darwin arm64 macho-arm64 "$SHA"
}

verify_case_macos "a Linux ELF in the macOS slot -> FAIL" "is not a 64-bit little-endian Mach-O" \
  sing-box-darwin-arm64 BUILD-INFO-MACOS.txt darwin arm64 elf-x86-64 "$SHA"
verify_case_macos "missing macOS .sha256 -> FAIL" "sing-box-darwin-arm64.sha256: found 0 copies" \
  sing-box-darwin-arm64 BUILD-INFO-MACOS.txt darwin arm64 macho-arm64 "$SHA" no-sha256
verify_case_macos "macOS BUILD-INFO from another commit -> FAIL" "built from a different commit" \
  sing-box-darwin-arm64 BUILD-INFO-MACOS.txt darwin arm64 macho-arm64 "$SHA" bad-commit

# A binary claiming the other platform's BUILD-INFO must fail on metadata.
verify_case "BUILD-INFO platform mismatch -> FAIL" "records platform" \
  sing-box-linux-amd64 BUILD-INFO-LINUX.txt darwin arm64 elf-x86-64 "$SHA"

# verify_case leaves the mutated fixture in place, so the good Linux artifact is
# restored here and re-downloaded, which is what makes the next case a control for
# the REAL artifact path rather than for a directory that happened to be good
# earlier. Forgetting the restore once made the dry-run case fail for the wrong
# reason.
make_artifact "$work/fixtures/linux" sing-box-linux-amd64 BUILD-INFO-LINUX.txt linux amd64 elf-x86-64 "$SHA"
write_scenario base_r3 "pass"
gate_setup "re-download after restoring the fixtures" plan --repo o/r --sha "$SHA" --out "$work/plan-r3.tsv" --interval 1 --timeout 10
gate_setup "re-download after restoring the fixtures" download --repo o/r --plan "$work/plan-r3.tsv" --dir "$work/assets-r3"
expect_ok "the restored fixtures verify again (control)" verify --desktop-dir "$DESKTOP_DIR" --version 0.1 --dir "$work/assets-r3" --sha "$SHA"

# ---------------------------------------------------------------------------
# Case 7: the publish decision fails closed
# ---------------------------------------------------------------------------

expect_ok "dry_run=true -> publish=false" publish-flag --dry-run true
if [ "$(run_gate publish-flag --dry-run true)" = "false" ] \
   && [ "$(run_gate publish-flag --dry-run false)" = "true" ]; then
  report "dry_run maps to the publish flag correctly" 0 0 "true->false, false->true"
else
  report "dry_run maps to the publish flag correctly" 0 1
fi
expect_fail "an empty dry_run -> FAIL (fail closed, no publish)" "must be exactly 'true' or 'false'" -- \
  publish-flag --dry-run ""
expect_fail "a malformed dry_run -> FAIL (fail closed, no publish)" "must be exactly 'true' or 'false'" -- \
  publish-flag --dry-run yes

# ---------------------------------------------------------------------------
# Case 8: tags
# ---------------------------------------------------------------------------
#
# Exercised against a real throwaway git repository, so the rev-parse path is the
# production one.

tagrepo="$work/tagrepo"
mkdir -p "$tagrepo"
(
  cd "$tagrepo"
  git init -q .
  git config user.email t@example.invalid
  git config user.name test
  echo one > f
  git add f
  git commit -qm "first"
  git tag v0.1
  echo two > f
  git commit -qam "second"
  git tag v0.2
  git checkout -q v0.1
)

tag_gate() { ( cd "$tagrepo" && "$gate" check-tag "$@" ); }

rc=0; out="$(tag_gate --tag v0.1 2>&1)" || rc=$?
report "a valid tag at the checked-out commit resolves" 0 "$rc" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-60)"

rc=0; out="$(tag_gate --tag v0.2 2>&1)" || rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "but the checked-out commit is"; then
  report "a tag pointing at another commit is refused" nonzero "$rc"
else
  report "a tag pointing at another commit is refused" nonzero "$rc" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-60)"
fi

for bad in "0.1" "v" "release-1" "v0.1;rm -rf /" 'v0.1$(id)' 'v0.1`id`' "v0.1/../../x" "v0.1..2" "v0.1 x"; do
  rc=0; out="$(tag_gate --tag "$bad" 2>&1)" || rc=$?
  if [ "$rc" -ne 0 ]; then
    report "unsafe tag '$bad' is refused" nonzero "$rc"
  else
    report "unsafe tag '$bad' is refused" nonzero "$rc" "it was ACCEPTED"
  fi
done

rc=0; out="$(tag_gate --tag v9.9 2>&1)" || rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "does not exist"; then
  report "an unresolvable tag is refused" nonzero "$rc"
else
  report "an unresolvable tag is refused" nonzero "$rc" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-60)"
fi

# ---------------------------------------------------------------------------
# Case 9: the workflow itself
# ---------------------------------------------------------------------------
#
# The logic above is only reachable if release.yml is wired to it. These are
# static assertions on the YAML: cheap, and they fail if the fix is undone.

wf="$repo_root/.github/workflows/release.yml"

check_wf() { # <name> <expected 0|nonzero> <grep -c count>
  local name="$1" want="$2" got="$3"
  if [ "$want" = "$got" ]; then report "$name" 0 0; else report "$name" 0 1 "count=$got want=$want"; fi
}

count="$(grep -v '^[[:space:]]*#' "$wf" | grep -c 'gh run list' || true)"
check_wf "release.yml no longer uses 'gh run list'" 0 "$count"
count="$(grep -v '^[[:space:]]*#' "$wf" | grep -c '|| true' || true)"
check_wf "release.yml has no '|| true' escape hatch" 0 "$count"
count="$(grep -v '^[[:space:]]*#' "$wf" | grep -c 'gh release create' || true)"
check_wf "release.yml creates the release in exactly one place" 1 "$count"
count="$(grep -c 'release-artifacts.sh' "$wf" || true)"
if [ "$count" -ge 5 ]; then report "release.yml delegates to the tested gate (>=5 call sites)" 0 0 "count=$count"
else report "release.yml delegates to the tested gate (>=5 call sites)" 0 1 "count=$count"; fi

# The tag and the dry-run flag reach the scripts through the environment, never
# interpolated into a shell body: `${{ inputs.tag }}` inside `run:` is a
# script-injection sink. The only legitimate uses are an `env:` entry or a
# `with: ref:` entry, both of which pass the value as data.
bad_tag_lines="$(grep -n 'inputs\.' "$wf" \
  | grep -vE '^[0-9]+: *#' \
  | grep -vE '^[0-9]+: *([A-Z_]+|ref): \$\{\{ inputs\.[a-z_]+ \}\}$' || true)"
if [ -z "$bad_tag_lines" ]; then
  report "inputs.* is passed as data, never interpolated into run:" 0 0
else
  report "inputs.* is passed as data, never interpolated into run:" 0 1 "$bad_tag_lines"
fi

# The publish step must be gated on the computed flag, not on the raw input.
if grep -q "if: \\\${{\${{ steps.plan.outputs.publish == 'true' }}\}}" "$wf" 2>/dev/null \
   || grep -q "steps.plan.outputs.publish == 'true'" "$wf"; then
  report "the publish step is gated on the computed publish flag" 0 0
else
  report "the publish step is gated on the computed publish flag" 0 1
fi

# The gate test must run before the release acts, and on every push.
if grep -q 'test-release-artifacts.sh' "$wf" && grep -q 'test-release-artifacts.sh' "$repo_root/.github/workflows/verify.yml"; then
  report "the gate test runs in release.yml and in verify.yml" 0 0
else
  report "the gate test runs in release.yml and in verify.yml" 0 1
fi

# ---------------------------------------------------------------------------
# Case 10: a full dry run must not publish
# ---------------------------------------------------------------------------
#
# The end-to-end sequence of release.yml with dry_run=true. The mock records any
# `gh release create`; there must be none.

write_scenario dryrun "pass"
rm -f "$work/state/release_created"
make_desktop "$work/dry-desktop" "0.1" "$SHA"
gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/dry.tsv" --interval 1 --timeout 10
gate_setup "download (setup)" download --repo o/r --plan "$work/dry.tsv" --dir "$work/dry-assets"
gate_setup "verify (setup)" verify --desktop-dir "$work/dry-desktop" --version 0.1 --dir "$work/dry-assets" --sha "$SHA"
gate_setup "flatten (setup)" flatten --plan "$work/dry.tsv" --dir "$work/dry-assets" --out "$work/dry-upload" \
  --tag v0.1 --desktop "$work/dry-desktop/Jiejiebox-v0.1-windows-x64.exe" --version 0.1
gate_setup "check-upload (setup)" check-upload --out "$work/dry-upload" --tag v0.1 --version 0.1
publish="$(run_gate publish-flag --dry-run true)"
if [ "$publish" = "false" ] && [ ! -f "$work/state/release_created" ]; then
  report "a fully verified dry run does NOT call gh release create" 0 0
else
  report "a fully verified dry run does NOT call gh release create" 0 1 "publish=$publish"
fi

# And the positive control: with the flag false, the publish command is reachable.
if [ "$(run_gate publish-flag --dry-run false)" = "true" ]; then
  PATH="$work/bin:$PATH" MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
    "$work/bin/gh" release create v0.1 --title v0.1 >/dev/null 2>&1
  if [ -f "$work/state/release_created" ]; then
    report "the publish path is reachable when dry_run=false (control)" 0 0
  else
    report "the publish path is reachable when dry_run=false (control)" 0 1
  fi
fi

# A dry run that finds a FAILURE must still fail, and still not publish.
write_scenario dryrun_bad \
  'scenario["workflows"][macos_wf]["runs"][-1] = run(9199, 29, conclusion="failure", workflow_id=macos_id)'
rm -f "$work/state/release_created"
rc=0
# Deliberately NOT gate_setup: here the FAILURE is the expected outcome, so the
# exit code has to be observed rather than absorbed.
run_gate plan --repo o/r --sha "$SHA" --out "$work/drybad.tsv" --interval 1 --timeout 5 >/dev/null 2>&1 || rc=$?
if [ "$rc" -ne 0 ] && [ ! -f "$work/state/release_created" ]; then
  report "a dry run over failing CI fails and does not publish" 0 0
else
  report "a dry run over failing CI fails and does not publish" 0 1 "rc=$rc"
fi

# ---------------------------------------------------------------------------
# Case 11: the API is queried by SHA and by workflow id, never by name
# ---------------------------------------------------------------------------

write_scenario base_c "pass"
rm -f "$work/state/calls.log"
gate_setup "plan (setup)" plan --repo o/r --sha "$SHA" --out "$work/plan-c.tsv" --interval 1 --timeout 10
if grep -q "runs?head_sha=$SHA" "$work/state/calls.log"; then
  report "runs are queried with head_sha=<commit>" 0 0
else
  report "runs are queried with head_sha=<commit>" 0 1 "$(tr '\n' ' ' < "$work/state/calls.log")"
fi
if grep -qE "workflows/$LINUX_ID/runs" "$work/state/calls.log" \
   && grep -qE "workflows/$MACOS_ID/runs" "$work/state/calls.log"; then
  report "runs are queried by numeric workflow id, not by display name" 0 0
else
  report "runs are queried by numeric workflow id, not by display name" 0 1 "$(tr '\n' ' ' < "$work/state/calls.log")"
fi
if grep -q "UNEXPECTED" "$work/state/calls.log"; then
  report "no unexpected gh invocation" 0 1 "$(grep UNEXPECTED "$work/state/calls.log")"
else
  report "no unexpected gh invocation" 0 0
fi
# The old implementation listed runs repository-wide and filtered locally, which
# is why 60 rows of unrelated activity could hide the target. Every runs query
# must be filtered by SHA on the server side.
unfiltered="$(grep 'api .*runs' "$work/state/calls.log" | grep -v 'head_sha=' || true)"
if [ -z "$unfiltered" ]; then
  report "every runs query is filtered by head_sha server-side" 0 0
else
  report "every runs query is filtered by head_sha server-side" 0 1 "$unfiltered"
fi

# A renamed workflow must be caught by the path assertion, not silently resolved.
write_scenario moved \
  'scenario["workflows"][linux_wf]["path"] = ".github/workflows/moved-linux.yml"'
expect_fail "a workflow that moved is refused, not silently used" "must be updated there deliberately" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-moved.tsv" --interval 1 --timeout 5

write_scenario inactive 'scenario["workflows"][linux_wf]["state"] = "disabled_manually"'
expect_fail "a disabled workflow is refused" "not active" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-off.tsv" --interval 1 --timeout 5

# ---------------------------------------------------------------------------
# Case 12: the API failing outright must be loud
# ---------------------------------------------------------------------------

write_scenario gone 'scenario["workflows"].pop(linux_wf)'
expect_fail "an unresolvable workflow id -> FAIL" "cannot resolve workflow" -- \
  plan --repo o/r --sha "$SHA" --out "$work/plan-gone.tsv" --interval 1 --timeout 5

# ---------------------------------------------------------------------------
# Case 13: the FULL public asset contract - four products and the desktop installer
# ---------------------------------------------------------------------------
#
# Everything above runs the lightweight two-product fixture, which keeps the general cases
# focused and is deliberately left alone. It cannot prove the SHAPE OF A RELEASE, though:
# a release publishes four core platforms plus the Windows desktop installer, and the
# contract check, the manifest and the asset list are all sized by that number.
#
# So this case builds the real shape. It is the only one that exercises
# RELEASE_EXTRA_PLATFORMS=true, which is what release.yml sets, and the only one whose
# expected counts are the release's own: 6 files, 5 manifest entries.
#
# The point is not the counts. It is that the helper is executed against the real
# deliverable set before a release is, rather than the two-product subset this suite uses
# everywhere else.

# The four products release.yml enables, with the fields target_field declares for them.
# Kept as data so a product cannot be added to the gate's table and forgotten here: the
# manifest assertions below are derived from this list.
FULL_PRODUCTS="
linux|.github/workflows/server-linux-amd64.yml|sing-box-linux-amd64|BUILD-INFO-LINUX.txt|linux|amd64|elf-x86-64|jiejie-sing-box-linux-amd64|7001
macos|.github/workflows/client-macos.yml|sing-box-darwin-arm64|BUILD-INFO-MACOS.txt|darwin|arm64|macho-arm64|jiejie-sing-box-macos-arm64|7002
windows|.github/workflows/windows-core-amd64.yml|sing-box.exe|BUILD-INFO-WINDOWS.txt|windows|amd64|pe-amd64|jiejie-sing-box-windows-amd64|7003
android|.github/workflows/android-core-arm64.yml|libbox.aar|BUILD-INFO-ANDROID.txt|android|arm64|aar-arm64|jiejie-sing-box-android-arm64|7004
"
FULL_VERSION="0.1.6"
FULL_TAG="v$FULL_VERSION"
FULL_DESKTOP_ID=7005

# run_gate_full <args...> - the gate as release.yml runs it: every platform enabled.
run_gate_full() {
  PATH="$work/bin:$PATH" MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
    RELEASE_EXTRA_PLATFORMS=true "$gate" "$@"
}

# make_full_artifacts - one verified artifact directory per product, plus the installer.
make_full_artifacts() {
  rm -rf "$work/full"
  mkdir -p "$work/full/artifacts"
  local row
  # `printf | while read` rather than a here-string: the list is static and this keeps the
  # block parseable by a POSIX shell, which is how the syntax of this file is checked on a
  # host without bash. The list is written with a leading newline for readability, so empty
  # rows are skipped rather than turned into a product with no fields.
  printf '%s\n' "$FULL_PRODUCTS" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    # `set --` rather than a here-string: this file's syntax is checked by a POSIX shell on
    # hosts without bash, and `<<<` is a bash extension.
    old_ifs="$IFS"; IFS='|'; set -- $row; IFS="$old_ifs"
    [ "$#" -eq 9 ] || { echo "FAIL: malformed FULL_PRODUCTS row ($# fields): $row" >&2; exit 1; }
    make_artifact "$work/full/artifacts/$1" "$3" "$4" "$5" "$6" "$7" "$SHA"
  done
  make_desktop "$work/full/desktop" "$FULL_VERSION" "$SHA"
}

# write_full_scenario - a scenario in which all four products and the desktop have a
# successful run for this commit.
write_full_scenario() {
  MOCK_STATE="$work/state" MOCK_FIXTURES="$work/fixtures" \
    python3 - "$SHA" "$FULL_PRODUCTS" "$FULL_DESKTOP_ID" <<'PY'
import json, os, pathlib, sys
sha, products, desktop_id = sys.argv[1], sys.argv[2], int(sys.argv[3])
state = pathlib.Path(os.environ["MOCK_STATE"])
state.mkdir(parents=True, exist_ok=True)
for stale in state.glob("count-*"):
    stale.unlink()
for stale in ("release_created", "calls.log"):
    p = state / stale
    if p.exists():
        p.unlink()

def run(rid, number, workflow_id):
    return {"id": rid, "run_number": number, "status": "completed", "conclusion": "success",
            "created_at": "2026-10-08T10:%02d:00Z" % (number % 60),
            "html_url": "https://example.invalid/runs/%d" % rid,
            "head_sha": sha, "workflow_id": workflow_id}

def wf(wid, name, runs):
    return {"id": wid, "path": ".github/workflows/" + name, "state": "active",
            "name": name, "runs": runs}

workflows = {}
artifacts = {}
for i, row in enumerate([r for r in products.strip().splitlines() if r.strip()]):
    key, path, _bin, _info, _plat, _arch, _magic, _tar, rid = row.split("|")
    wid = 8000 + i
    workflows[os.path.basename(path)] = wf(wid, os.path.basename(path), [run(int(rid), 10 + i, wid)])
    artifacts[rid] = {"dir": key, "name": "Jiejie-%s-%s" % (key, sha[:7])}

workflows["client-desktop-windows.yml"] = wf(
    8010, "client-desktop-windows.yml", [run(desktop_id, 30, 8010)])
artifacts[str(desktop_id)] = {"dir": "desktop", "name": "jiejiebox-windows-x64-signed-test"}

(state / "scenario.json").write_text(json.dumps(
    {"workflows": workflows, "artifacts": artifacts}, indent=1))
PY
}

make_full_artifacts
write_full_scenario

# `run_gate_full` sets RELEASE_EXTRA_PLATFORMS=true - the value release.yml sets. Without
# it the gate targets two products and a four-product release is not what is being tested.

# full_expect_ok <name> <args...>
full_expect_ok() {
  local name="$1"; shift
  local out rc=0
  out="$(run_gate_full "$@" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    report "$name" 0 "$rc" ""
  else
    report "$name" 0 "$rc" ""
    printf '      %s\n' "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-600)"
  fi
}

# full_expect_fail <name> <substring> -- <args...>
full_expect_fail() {
  local name="$1" want="$2"; shift 2
  [ "${1:-}" = "--" ] && shift
  local out rc=0
  out="$(run_gate_full "$@" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    fail_case "$name" "it SUCCEEDED; the command must have failed"
    return
  fi
  if printf '%s' "$out" | grep -qF -- "$want"; then
    report "$name" nonzero "$rc" "refused: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-56)"
  else
    fail_case "$name" "refused for the WRONG reason; wanted '$want'"
    printf '      actual: %s\n' "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-400)"
  fi
}

full_plan="$work/full-plan.tsv"
full_expect_ok "full contract: plan sees all four products" \
  plan --repo o/r --sha "$SHA" --out "$full_plan" --interval 1 --timeout 10
full_expect_ok "full contract: download all four products" \
  download --repo o/r --plan "$full_plan" --dir "$work/full/assets"
full_expect_ok "full contract: stage the desktop installer" \
  desktop --repo o/r --sha "$SHA" --out "$work/full/desktop-dl" --interval 1 --timeout 10
full_expect_ok "full contract: verify four products and the installer" \
  verify --dir "$work/full/assets" --sha "$SHA" \
    --desktop-dir "$work/full/desktop" --version "$FULL_VERSION"
full_expect_ok "full contract: flatten -> PASS" \
  flatten --plan "$full_plan" --dir "$work/full/assets" --out "$work/full/upload" \
    --tag "$FULL_TAG" --desktop "$work/full/desktop/Jiejiebox-v$FULL_VERSION-windows-x64.exe" \
    --version "$FULL_VERSION"
full_expect_ok "full contract: check-upload -> PASS" \
  check-upload --out "$work/full/upload" --tag "$FULL_TAG" --version "$FULL_VERSION"

# The published set is exactly the six public deliverables.
full_expected="Jiejiebox-v$FULL_VERSION-windows-x64.exe
jiejie-sing-box-linux-amd64-$FULL_TAG.tar.gz
jiejie-sing-box-macos-arm64-$FULL_TAG.tar.gz
jiejie-sing-box-windows-amd64-$FULL_TAG.tar.gz
jiejie-sing-box-android-arm64-$FULL_TAG.tar.gz
SHA256SUMS"

full_listing="$(find "$work/full/upload" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | sort)"
if [ "$full_listing" = "$(printf '%s\n' "$full_expected" | sort)" ]; then
  report "full contract: the staging directory is exactly the six public files" 0 0
else
  report "full contract: the staging directory is exactly the six public files" 0 1 "got: $full_listing"
fi

if [ "$(find "$work/full/upload" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')" = "6" ]; then
  report "full contract: public file count is 6" 0 0
else
  report "full contract: public file count is 6" 0 1 \
    "got $(find "$work/full/upload" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')"
fi

full_entries="$(grep -c . "$work/full/upload/SHA256SUMS")"
if [ "$full_entries" = "5" ]; then
  report "full contract: SHA256SUMS has exactly 5 entries" 0 0
else
  report "full contract: SHA256SUMS has exactly 5 entries" 0 1 "got $full_entries"
fi

# The manifest's SHAPE, not just its length. Every line must be a bare filename naming one
# of the five binaries: no path prefix, no './', and none of the CI records that live in the
# desktop artifact directory - those are provenance, not downloads.
full_named="$(sed -n 's/^[0-9a-f]\{64\}  //p' "$work/full/upload/SHA256SUMS" | sort)"
full_want_named="$(printf '%s\n' \
  "Jiejiebox-v$FULL_VERSION-windows-x64.exe" \
  "jiejie-sing-box-linux-amd64-$FULL_TAG.tar.gz" \
  "jiejie-sing-box-macos-arm64-$FULL_TAG.tar.gz" \
  "jiejie-sing-box-windows-amd64-$FULL_TAG.tar.gz" \
  "jiejie-sing-box-android-arm64-$FULL_TAG.tar.gz" | sort)"
if [ "$full_named" = "$full_want_named" ]; then
  report "full contract: the manifest names exactly the five public binaries" 0 0
else
  report "full contract: the manifest names exactly the five public binaries" 0 1 "got: $full_named"
fi

full_shape_bad=""
if [ "$(printf '%s\n' "$full_named" | grep -c '^\./')" != "0" ]; then full_shape_bad="$full_shape_bad ./-prefix"; fi
if [ "$(printf '%s\n' "$full_named" | grep -c '/')" != "0" ]; then full_shape_bad="$full_shape_bad path-separator"; fi
for forbidden in BUILD-INFO sha256 SHA256SUMS debug records runner-environment; do
  if printf '%s\n' "$full_named" | grep -qi -- "$forbidden"; then
    full_shape_bad="$full_shape_bad $forbidden"
  fi
done
if [ -z "$full_shape_bad" ]; then
  report "full contract: no CI record, sidecar, self-entry or path in the manifest" 0 0
else
  report "full contract: no CI record, sidecar, self-entry or path in the manifest" 0 1 "found:$full_shape_bad"
fi

# And it must verify in the directory it describes - the command the release notes give a
# downloader.
if ( cd "$work/full/upload" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ); then
  report "full contract: sha256sum -c passes over the five binaries" 0 0
else
  report "full contract: sha256sum -c passes over the five binaries" 0 1 "manifest does not verify"
fi

# --- negative: a missing product must not publish --------------------------
#
# The release-tree check is rebuilt with Android's artifact directory removed. A release
# that silently shipped three cores and a desktop would otherwise pass every check, so
# `verify` must refuse - and name the deliverable that is missing, not merely fail.
rm -rf "$work/full/assets-noandroid"
cp -R "$work/full/assets" "$work/full/assets-noandroid"
rm -rf "$work/full/assets-noandroid/libbox.aar"
full_expect_fail "full contract: a missing Android deliverable -> FAIL" \
  "libbox.aar: found 0 copies" -- \
  verify --dir "$work/full/assets-noandroid" --sha "$SHA" \
    --desktop-dir "$work/full/desktop" --version "$FULL_VERSION"

# --- negative: a CI record must not reach the download page ---------------
#
# The desktop artifact directory carries BUILD-INFO-WINDOWS-DESKTOP - it is what proves the
# installer's core, and it stays in Actions artifacts. Dropped into staging it is an
# "unexpected public asset" and must be refused, which is the whole point of the allowlist.
cp "$work/full/desktop/BUILD-INFO-WINDOWS-DESKTOP-$FULL_VERSION.txt" \
   "$work/full/upload/BUILD-INFO-WINDOWS-DESKTOP-$FULL_VERSION.txt"
full_expect_fail "full contract: an extra BUILD-INFO in staging -> FAIL" \
  "not public deliverable" -- \
  check-upload --out "$work/full/upload" --tag "$FULL_TAG" --version "$FULL_VERSION"
rm -f "$work/full/upload/BUILD-INFO-WINDOWS-DESKTOP-$FULL_VERSION.txt"

# --- negative: a single-file checksum sidecar is not a public asset --------
if [ "${FULL_CONTRACT_EXTRA_NEGATIVES:-true}" = "true" ]; then
  printf 'placeholder\n' > "$work/full/upload/Jiejiebox-v$FULL_VERSION-windows-x64.exe.sha256"
  full_expect_fail "full contract: an extra .sha256 sidecar in staging -> FAIL" \
    "not public deliverable" -- \
    check-upload --out "$work/full/upload" --tag "$FULL_TAG" --version "$FULL_VERSION"
  rm -f "$work/full/upload/Jiejiebox-v$FULL_VERSION-windows-x64.exe.sha256"

  printf 'placeholder\n' > "$work/full/upload/SHA256SUMS-WINDOWS-DESKTOP-$FULL_TAG.txt"
  full_expect_fail "full contract: an extra SHA256SUMS-WINDOWS-DESKTOP file -> FAIL" \
    "not public deliverable" -- \
    check-upload --out "$work/full/upload" --tag "$FULL_TAG" --version "$FULL_VERSION"
  rm -f "$work/full/upload/SHA256SUMS-WINDOWS-DESKTOP-$FULL_TAG.txt"
fi

# ---------------------------------------------------------------------------

echo
if [ "$failures" -eq 0 ]; then
  echo "PASS: all $cases cases behaved as required"
  exit 0
fi
echo "FAIL: $failures of $cases cases did not behave as required" >&2
exit 1
