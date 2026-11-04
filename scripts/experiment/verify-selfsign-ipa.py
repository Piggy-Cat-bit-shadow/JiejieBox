#!/usr/bin/env python3
"""Static contract for the phase-1 self-sign experiment IPAs.

The build script proves the artifact was produced; this proves it is the artifact the
experiment claims. Every assertion here is something a wrong build configuration has
silently produced before, in this repository or in the package under test:

  * the file name said 0.1.37 while the app said 1.14.8, because the unsigned build never
    passed MARKETING_VERSION;
  * the identifiers were upstream's io.nekohasekai.* because the unsigned build never passed
    BASE_PACKAGE_IDENTIFIER;
  * an extension kept its own version, which makes "the app is 0.1.37" ambiguous;
  * a `_CodeSignature` directory survived an unsigned build and made the package look signed.

Usage:
    python3 scripts/experiment/verify-selfsign-ipa.py \
        --version 0.1.37 --build 137 --base io.jiejiebox.app --group group.io.jiejiebox.app \
        [--diagnostic] path/to/app.ipa
"""

import argparse
import plistlib
import sys
import zipfile


def load(archive, name):
    with archive.open(name) as handle:
        return plistlib.load(handle)


def bundle_prefixes(names):
    app = [name for name in names if name.startswith("Payload/") and name.endswith(".app/Info.plist")]
    if len(app) != 1:
        return None, []
    prefix = app[0][: -len("/Info.plist")]
    extensions = [
        name[: -len("/Info.plist")]
        for name in names
        if name.startswith(prefix + "/PlugIns/") and name.endswith(".appex/Info.plist")
    ]
    return prefix, extensions


def verify(path, version, build, base, group, diagnostic):
    problems = []
    notes = []
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        prefix, extensions = bundle_prefixes(names)
        if not prefix:
            return [f"{path}: expected exactly one Payload/*.app"], notes
        app_info = load(archive, prefix + "/Info.plist")

        def expect_equal(label, actual, wanted):
            if actual != wanted:
                problems.append(f"{path}: {label} is {actual!r}, expected {wanted!r}")

        expect_equal("CFBundleIdentifier", app_info.get("CFBundleIdentifier"), base)
        expect_equal("CFBundleShortVersionString", app_info.get("CFBundleShortVersionString"), version)
        expect_equal("CFBundleVersion", app_info.get("CFBundleVersion"), build)
        expect_equal("CFBundleDisplayName", app_info.get("CFBundleDisplayName"), "JiejieBox")
        expect_equal("BasePackageIdentifier", app_info.get("BasePackageIdentifier"), base)
        expect_equal("AppGroupIdentifier", app_info.get("AppGroupIdentifier"), group)

        if not app_info.get("NSExtension") is None:
            problems.append(f"{path}: the app target declares an NSExtension; it is not an extension")

        if diagnostic:
            if len(extensions) != 1:
                problems.append(f"{path}: diagnostic layout has {len(extensions)} extensions, expected 1")
        elif len(extensions) < 2:
            problems.append(f"{path}: full layout has {len(extensions)} extensions, expected at least 2")

        tunnel = 0
        for extension in extensions:
            info = load(archive, extension + "/Info.plist")
            identifier = info.get("CFBundleIdentifier") or ""
            if not identifier.startswith(base + "."):
                problems.append(
                    f"{path}: {extension.split('/')[-1]} bundle identifier is {identifier!r}, "
                    f"which is outside the {base} family a signer has to be able to remap"
                )
            expect_equal(f"{extension.split('/')[-1]} CFBundleShortVersionString", info.get("CFBundleShortVersionString"), version)
            expect_equal(f"{extension.split('/')[-1]} CFBundleVersion", info.get("CFBundleVersion"), build)
            expect_equal(f"{extension.split('/')[-1]} BasePackageIdentifier", info.get("BasePackageIdentifier"), base)
            point = (info.get("NSExtension") or {}).get("NSExtensionPointIdentifier")
            if point == "com.apple.networkextension.packet-tunnel":
                tunnel += 1
        if tunnel != 1:
            problems.append(f"{path}: found {tunnel} packet-tunnel extensions, expected exactly 1")

        residue = [name for name in names if "/_CodeSignature/" in name or name.endswith("/embedded.mobileprovision")]
        if residue:
            problems.append(f"{path}: unsigned build carries signature residue: {residue[:3]}")

        frameworks = sorted({name.split("/")[3] for name in names if name.startswith(prefix + "/Frameworks/") and name.count("/") > 4})
        if "Library.framework" not in frameworks:
            problems.append(f"{path}: Library.framework is missing from Frameworks/ ({frameworks})")
        else:
            binary = f"{prefix}/Frameworks/Library.framework/Library"
            if binary not in names:
                problems.append(f"{path}: Library.framework has no Library binary")
            else:
                header = archive.read(binary)[:8]
                if header[:4] not in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xca\xfe\xba\xbe"):
                    problems.append(f"{path}: Library.framework/Library is not a Mach-O ({header.hex()})")
        notes.append(f"  {path}: {len(extensions)} extensions, frameworks={frameworks}")
    return problems, notes


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--build", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--group", required=True)
    parser.add_argument("--diagnostic", action="store_true")
    parser.add_argument("ipa", nargs="+")
    arguments = parser.parse_args()

    problems = []
    for path in arguments.ipa:
        found, notes = verify(path, arguments.version, arguments.build, arguments.base, arguments.group, arguments.diagnostic)
        for note in notes:
            print(note)
        problems.extend(found)
    if problems:
        print("FAIL: the artifact does not satisfy the self-sign contract", file=sys.stderr)
        for problem in problems:
            print(f"  - {problem}", file=sys.stderr)
        return 1
    print("PASS: every artifact satisfies the self-sign contract")
    return 0


if __name__ == "__main__":
    sys.exit(main())
