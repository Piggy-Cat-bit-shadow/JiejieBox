#!/usr/bin/env python3
"""Checks that the Windows client's machine-global identity agrees with itself.

Usage: verify-desktop-identity-consistency.py [--client-dir <path>] [--core-dir <path>]

Exit: 0 = every copy agrees, 1 = a copy disagrees or disappeared.

# Why this exists

The same handful of names has to appear in four places that cannot import each other:

  clients/desktop/src/main/identity.ts        the runtime, and the single place the names
                                              are explained
  clients/desktop/build/installer.nsh         NSIS cannot read TypeScript
  clients/desktop/build/installer-*.ps1       PowerShell, invoked by NSIS
  experimental/boxdd/service_name.go          the daemon, a separate Go program

Each of these names is a KEY in a machine-wide namespace - an SCM service name, a named
pipe path, a registry path, an AppData directory. Every one of them is therefore also a
place where this client and the upstream client can collide, which is the entire point of
the change that introduced them. A disagreement does not fail loudly at build time: it
produces an installation whose runtime reads a layout key nobody wrote, whose installer
stops a service that is not its own, or whose GUI cannot reach its own daemon.

That is the failure mode this file exists to make impossible. It is a text check on
purpose: NSIS, PowerShell, TypeScript and Go have no common import, so the contract can only
be asserted where all four are visible, which is here.

# What it deliberately does NOT check

Names that are intentionally NOT isolated: `sing-box.exe`, `sing-box-daemon.exe`, the
`.bpf` association and the `sing-box://` scheme. Asserting the absence of `sing-box` would
be asserting the opposite of the design - see the identity map in
clients/desktop/src/main/identity.ts for why each of those is shared on purpose.
"""
import argparse
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))

DEFAULT_CORE_DIR = REPO
DEFAULT_CLIENT_DIR = os.path.join(REPO, "clients", "desktop")

# The spelling the fork's identity uses. Checked as a value, never as a substring of a
# larger word, so a hypothetical `jiejieboxx` would not pass.
BRAND = "jiejiebox"

failures = []
checked = 0


def read(path, label):
    try:
        with open(path, encoding="utf-8", newline=None) as stream:
            return stream.read()
    except OSError as error:
        failures.append(f"{label}: cannot read {path}: {error}")
        return None


def require(label, source, pattern, expected, description):
    """Asserts that `pattern` occurs in `source` capturing exactly `expected`."""
    global checked
    if source is None:
        return None
    match = re.search(pattern, source, re.MULTILINE)
    if match is None:
        failures.append(
            f"{label}: no match for {pattern!r} - {description} has been removed or rewritten"
        )
        return None
    actual = match.group(1)
    checked += 1
    if actual != expected:
        failures.append(
            f"{label}: {description} is {actual!r} but identity.ts says {expected!r}"
        )
    return actual


def main():
    parser = argparse.ArgumentParser(
        prog="verify-desktop-identity-consistency.py",
        description="Check that the Windows client's machine-global identity agrees.",
    )
    parser.add_argument("--client-dir", default=DEFAULT_CLIENT_DIR)
    parser.add_argument("--core-dir", default=DEFAULT_CORE_DIR)
    args = parser.parse_args()

    client = os.path.realpath(args.client_dir)
    core = os.path.realpath(args.core_dir)

    if not os.path.isdir(client):
        print(f"verify-desktop-identity-consistency: {client} is not a directory", file=sys.stderr)
        return 1

    identity_path = os.path.join(client, "src", "main", "identity.ts")
    identity = read(identity_path, "identity.ts")
    if identity is None:
        for failure in failures:
            print(f"FAIL  {failure}", file=sys.stderr)
        return 1

    # --- the runtime's own declarations, which everything else is compared against ------
    def ts_value(name, pattern):
        match = re.search(pattern, identity)
        if match is None:
            failures.append(f"identity.ts: {name} is gone; nothing can be compared against it")
            return None
        return match.group(1)

    application_identity = ts_value(
        "applicationIdentity", r'export const applicationIdentity = "([^"]+)"'
    )
    app_data_directory = ts_value(
        "applicationDataDirectoryName",
        r'export const applicationDataDirectoryName = ([A-Za-z]+)',
    )
    daemon_data_directory = ts_value(
        "daemonDataDirectoryName",
        r'export const daemonDataDirectoryName = `\$\{applicationIdentity\}([^`]*)`',
    )
    daemon_service_name = ts_value(
        "daemonServiceName", r'export const daemonServiceName = `([^`]+)`'
    )
    app_id_line = re.search(
        r'export const applicationUserModelId = `([^`]+)`', identity
    )
    if app_id_line is None:
        failures.append("identity.ts: applicationUserModelId is gone")
        app_id = None
    else:
        app_id = app_id_line.group(1).replace("${applicationIdentity}", application_identity or "")

    install_registry_path = ts_value(
        "installationRegistryPath",
        # `(.+?)` rather than a negated backtick class: a backtick inside this pattern is a
        # needless trap, and the delimiter is already pinned by `String.raw`.
        r"const installationRegistryPath = String\.raw.(.+?).;",
    )
    if install_registry_path is None:
        install_registry_path = ts_value(
            "installationRegistryPath", r"export const installationRegistryPath = String\.raw.(.+?).;"
        )
    installer_state_directory = ts_value(
        "installerStateDirectoryName",
        r'export const installerStateDirectoryName = `\$\{applicationIdentity\}([^`]*)`',
    )
    installation_id_stream = ts_value(
        "installationIdStreamName",
        r'export const installationIdStreamName = `\$\{applicationIdentity\}([^`]*)`',
    )
    registered_scheme = ts_value(
        "registeredDeepLinkScheme",
        r"export const registeredDeepLinkScheme = ([A-Za-z]+)",
    )
    taildrop_key = ts_value(
        "taildropVerbRegistryKey", r'export const taildropVerbRegistryKey = "([^"]+)"'
    )

    if application_identity != BRAND:
        failures.append(
            f"identity.ts: applicationIdentity is {application_identity!r}, expected {BRAND!r}"
        )

    # Derived values, so a rule below can name the composed string it expects.
    expected_daemon_name = f"{BRAND}{daemon_data_directory or ''}"
    expected_state_directory = f"{BRAND}{installer_state_directory or ''}"
    expected_stream = f"{BRAND}{installation_id_stream or ''}"
    expected_app_id = (
        f"io.github.piggycatbitshadow.{BRAND}" if app_id is not None else None
    )

    # --- electron-builder: the appId the runtime must match, and the executable name it
    #     must NOT have taken ------------------------------------------------------------
    builder = read(os.path.join(client, "electron-builder.yml"), "electron-builder.yml")
    require(
        "electron-builder.yml",
        builder,
        r"^[ \t]*appId:[ \t]*(\S+)[ \t]*$",
        expected_app_id,
        "win.appId (the uninstall key and the AUMID both derive from it)",
    )
    require(
        "electron-builder.yml",
        builder,
        r"^[ \t]*executableName:[ \t]*(\S+)[ \t]*$",
        "sing-box",
        "win.executableName (experimental/boxdd authenticates the app by this file name)",
    )
    # Only the `win:` block, and only a key assignment inside it.
    #
    # Two things would otherwise defeat this check. The comment above the appId quotes the
    # upstream value on purpose - to record what is being moved away from - and a check that
    # cannot tell a value from a description of one would forbid explaining the change. And
    # `linux.appId` is still `io.nekohasekai.sfl`, deliberately: this change is scoped to
    # Windows coexistence, where the two clients are actually installed side by side.
    if builder is not None:
        win_block = re.search(r"^win:\n((?:[ \t].*\n|\n)*)", builder, re.MULTILINE)
        if win_block is not None:
            assignments = "\n".join(
                line
                for line in win_block.group(1).splitlines()
                if not line.lstrip().startswith("#")
            )
            if re.search(r"^[ \t]*appId:[ \t]*io\.nekohasekai\.", assignments, re.MULTILINE):
                failures.append(
                    "electron-builder.yml: win.appId still names the upstream identity; the"
                    " two clients would share one uninstall entry and one AUMID"
                )

    # --- NSIS: the installer's copies ---------------------------------------------------
    nsh_path = os.path.join(client, "build", "installer.nsh")
    nsh = read(nsh_path, "installer.nsh")
    require(
        "installer.nsh",
        nsh,
        r'!define INSTALLATION_LAYOUT_REGISTRY_KEY "([^"]+)"',
        install_registry_path,
        "INSTALLATION_LAYOUT_REGISTRY_KEY",
    )
    require(
        "installer.nsh",
        nsh,
        r'!define DAEMON_DATA_DIRECTORY_NAME "([^"]+)"',
        expected_daemon_name,
        "DAEMON_DATA_DIRECTORY_NAME",
    )
    require(
        "installer.nsh",
        nsh,
        r'!define INSTALLER_STATE_DIRECTORY_NAME "([^"]+)"',
        expected_state_directory,
        "INSTALLER_STATE_DIRECTORY_NAME",
    )
    require(
        "installer.nsh",
        nsh,
        r'!define DEFINE_SERVICE_NAME "([^"]+)"',
        daemon_service_name,
        "DEFINE_SERVICE_NAME (must equal experimental/boxdd's serviceName)",
    )
    require(
        "installer.nsh",
        nsh,
        r'!define TAILDROP_VERB_REGISTRY_KEY "([^"]+)"',
        f"Software\\Classes\\*\\shell\\{taildrop_key}",
        "TAILDROP_VERB_REGISTRY_KEY",
    )

    # --- PowerShell: invoked by NSIS, and by the CI gates ------------------------------
    preflight = read(
        os.path.join(client, "build", "installer-preflight.ps1"), "installer-preflight.ps1"
    )
    require(
        "installer-preflight.ps1",
        preflight,
        r'^\$daemonServiceName = "([^"]+)"[ \t]*$',
        daemon_service_name,
        "the service whose SID the working-directory ACL is validated against",
    )
    require(
        "installer-preflight.ps1",
        preflight,
        r'\$DaemonWorkingDirectory = Join-Path \$commonApplicationData "([^"]+)"',
        expected_daemon_name,
        "the daemon working directory fallback",
    )

    data = read(os.path.join(client, "build", "installer-data.ps1"), "installer-data.ps1")
    require(
        "installer-data.ps1",
        data,
        r'^\$installerStateDirectoryName = "([^"]+)"[ \t]*$',
        expected_state_directory,
        "the data-migration state directory",
    )
    require(
        "installer-data.ps1",
        data,
        r'^\$installationIdStreamName = "([^"]+)"[ \t]*$',
        expected_stream,
        "the application data directory's installation-id stream",
    )

    # The two Windows PowerShell files above both used to carry the marker and the state
    # directory as literals in the middle of a function body. A leftover is a directory
    # that one half of the installer would stamp and the other half would not recognise.
    for label, source in (("installer-preflight.ps1", preflight), ("installer-data.ps1", data)):
        if source is None:
            continue
        for legacy in ("sing-box.installation-id", '"sing-box-installer"'):
            if legacy in source:
                failures.append(f"{label}: still carries {legacy!r}")

    # --- Go: the daemon's own copy ------------------------------------------------------
    go = read(os.path.join(core, "experimental", "boxdd", "service_name.go"), "service_name.go")
    require(
        "service_name.go",
        go,
        r'const serviceName = "([^"]+)"',
        daemon_service_name,
        "the daemon's service identity",
    )

    # --- report -------------------------------------------------------------------------
    if failures:
        print(
            "verify-desktop-identity-consistency: the Windows identity does not agree"
            " with itself. Nothing is written when this fails; it is a report.",
            file=sys.stderr,
        )
        for failure in failures:
            print(f"FAIL  {failure}", file=sys.stderr)
        return 1

    print("PASS: the Windows client's machine-global identity agrees across")
    print("      src/main/identity.ts, build/installer.nsh, build/installer-*.ps1")
    print("      and experimental/boxdd/service_name.go")
    print(f"  application identity   {application_identity}")
    print(f"  win.appId              {expected_app_id}")
    print(f"  application data       %APPDATA%\\{BRAND}")
    print(f"  daemon data            %ProgramData%\\{expected_daemon_name}")
    print(f"  daemon service / pipe  {daemon_service_name}")
    print(f"  installation layout    HKLM\\{install_registry_path}")
    print(f"  installer state        ...\\{expected_state_directory}\\data-transition.json")
    print(f"  installation id stream :{expected_stream}")
    print(f"  taildrop shell verb    {taildrop_key}")
    print(f"  registered scheme      {registered_scheme}://  (still parsing sing-box://)")
    print(f"  shared on purpose      sing-box.exe, sing-box-daemon.exe, .bpf, sing-box:// input")
    print(f"  {checked} values compared")
    return 0


if __name__ == "__main__":
    sys.exit(main())
