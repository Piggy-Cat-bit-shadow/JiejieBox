#!/usr/bin/env python3
"""Static audit of an iOS .ipa, for the self-sign compatibility experiment.

This is the reproducible version of the manual inspection that motivated phase 1: it walks the
ZIP layout, parses every Info.plist, parses each Mach-O's load commands (arch, platform, minimum
OS, and whether an LC_CODE_SIGNATURE is present), reports signature artefacts per bundle, and
extracts the entitlements an embedded provisioning profile grants.

It is deliberately dependency-free (stdlib only) so CI and a fresh checkout can run it. It is a
STATIC tool: it says what the package contains, never that the app runs.

Usage:
    python3 scripts/experiment/audit-ipa.py path/to/app.ipa [more.ipa ...]
    python3 scripts/experiment/audit-ipa.py --json path/to/app.ipa

Exit status is 0 when every input parses; findings are reported, not judged, by this tool.
"""

import json
import plistlib
import struct
import sys
import zipfile
from dataclasses import dataclass, field
from typing import Any

LC_CODE_SIGNATURE = 0x1D
LC_BUILD_VERSION = 0x32
LC_VERSION_MIN_IPHONEOS = 0x25
LC_VERSION_MIN_MACOSX = 0x24

CPU_TYPE_X86_64 = 0x01000007
CPU_TYPE_ARM64 = 0x0100000C
CPU_TYPE_ARM64_32 = 0x0200000C
CPU_TYPE_ARM = 0x0000000C

PLATFORM_NAMES = {
    1: "macos",
    2: "ios",
    3: "tvos",
    4: "watchos",
    5: "bridgeos",
    6: "maccatalyst",
    7: "iossimulator",
    8: "tvossimulator",
    9: "watchossimulator",
}

FAT_MAGIC = 0xCAFEBABE
FAT_CIGAM = 0xBEBAFECA
MH_MAGIC = 0xFEEDFACE
MH_CIGAM = 0xCEFAEDFE
MH_MAGIC_64 = 0xFEEDFACF
MH_CIGAM_64 = 0xCFFAEDFE

INTERESTING_PLIST_KEYS = [
    "CFBundleIdentifier",
    "CFBundleExecutable",
    "CFBundleName",
    "CFBundleDisplayName",
    "CFBundleShortVersionString",
    "CFBundleVersion",
    "CFBundlePackageType",
    "CFBundleSupportedPlatforms",
    "MinimumOSVersion",
    "DTPlatformName",
    "DTPlatformVersion",
    "DTSDKName",
    "UIDeviceFamily",
    "LSRequiresIPhoneOS",
    "UIRequiredDeviceCapabilities",
    "UIBackgroundModes",
    "ITSAppUsesNonExemptEncryption",
]


def decode_version(word: int) -> str:
    return f"{word >> 16}.{(word >> 8) & 0xFF}.{word & 0xFF}"


@dataclass
class MachO:
    arch: str = "unknown"
    cputype: int = 0
    platform: str = ""
    minos: str = ""
    sdk: str = ""
    has_code_signature: bool = False
    fat_slices: list[str] = field(default_factory=list)

    def as_dict(self) -> dict[str, Any]:
        return {
            "arch": self.arch,
            "cputype": self.cputype,
            "platform": self.platform,
            "minos": self.minos,
            "sdk": self.sdk,
            "code_signature": self.has_code_signature,
            "fat_slices": self.fat_slices,
        }


def arch_name(cputype: int) -> str:
    return {
        CPU_TYPE_ARM64: "arm64",
        CPU_TYPE_ARM64_32: "arm64_32",
        CPU_TYPE_ARM: "arm",
        CPU_TYPE_X86_64: "x86_64",
    }.get(cputype, f"cputype={cputype:#x}")


def parse_thin(data: bytes, offset: int, size: int, result: MachO) -> None:
    magic_be = struct.unpack_from(">I", data, offset)[0]
    # The magic itself tells you the byte order: 0xFEEDFACF is a big-endian file, 0xCFFAEDFE is
    # the same word stored little-endian.
    if magic_be in (MH_MAGIC_64, MH_MAGIC):
        little = False
    elif magic_be in (MH_CIGAM_64, MH_CIGAM):
        little = True
    else:
        return
    is64 = magic_be in (MH_MAGIC_64, MH_CIGAM_64)
    header_size = 32 if is64 else 28
    endian = "<" if little else ">"
    cputype = struct.unpack_from(endian + "i", data, offset + 4)[0] & 0xFFFFFFFF
    ncmds = struct.unpack_from(endian + "I", data, offset + 16)[0]

    result.arch = arch_name(cputype)
    result.cputype = cputype

    cursor = offset + header_size
    for _ in range(ncmds):
        if cursor + 8 > offset + size:
            break
        cmd, cmdsize = struct.unpack_from(endian + "II", data, cursor)
        if cmdsize < 8:
            break
        if cmd == LC_BUILD_VERSION:
            platform, minos, sdk = struct.unpack_from(endian + "III", data, cursor + 8)
            result.platform = PLATFORM_NAMES.get(platform, f"platform={platform}")
            result.minos = decode_version(minos)
            result.sdk = decode_version(sdk)
        elif cmd == LC_VERSION_MIN_IPHONEOS:
            minos, sdk = struct.unpack_from(endian + "II", data, cursor + 8)
            result.platform = result.platform or "ios"
            result.minos = result.minos or decode_version(minos)
            result.sdk = result.sdk or decode_version(sdk)
        elif cmd == LC_CODE_SIGNATURE:
            result.has_code_signature = True
        cursor += cmdsize


def parse_macho(data: bytes) -> MachO:
    result = MachO()
    if len(data) < 8:
        return result
    magic = struct.unpack_from(">I", data, 0)[0]
    if magic in (FAT_MAGIC, FAT_CIGAM):
        little = magic == FAT_CIGAM
        nfat = struct.unpack_from(("<I" if little else ">I"), data, 4)[0]
        for index in range(min(nfat, 8)):
            base = 8 + index * 20
            if base + 20 > len(data):
                break
            cputype, _sub, offset, size, _align = struct.unpack_from(
                ("<iiIII" if little else ">iiIII"), data, base
            )
            result.fat_slices.append(arch_name(cputype & 0xFFFFFFFF))
            if index == 0:
                parse_thin(data, offset, size, result)
        return result
    parse_thin(data, 0, len(data), result)
    return result


def find_zip_prefix(names: list[str]) -> str:
    for name in names:
        if name.startswith("Payload/") and name.endswith(".app/Info.plist"):
            return name[: -len("/Info.plist")]
    return ""


def plist_from_zip(archive: zipfile.ZipFile, name: str) -> dict[str, Any] | None:
    try:
        with archive.open(name) as handle:
            return plistlib.load(handle)
    except Exception:
        return None


def profile_entitlements(archive: zipfile.ZipFile, name: str) -> dict[str, Any] | None:
    """Extract the plist embedded in a CMS-wrapped .mobileprovision."""
    try:
        raw = archive.read(name)
    except KeyError:
        return None
    start = raw.find(b"<?xml")
    end = raw.find(b"</plist>")
    if start < 0 or end < 0:
        return None
    try:
        return plistlib.loads(raw[start : end + len(b"</plist>")])
    except Exception:
        return None


def describe_bundle(archive: zipfile.ZipFile, prefix: str, kind: str) -> dict[str, Any]:
    label = prefix.rsplit("/", 1)[-1]
    info = plist_from_zip(archive, prefix + "/Info.plist") or {}
    executable = info.get("CFBundleExecutable", "")
    entry: dict[str, Any] = {
        "kind": kind,
        "path": prefix,
        "name": label,
        "info": {key: info.get(key) for key in INTERESTING_PLIST_KEYS if key in info},
    }
    extension = info.get("NSExtension")
    if isinstance(extension, dict):
        entry["extension"] = {
            "point": extension.get("NSExtensionPointIdentifier"),
            "principal": extension.get("NSExtensionPrincipalClass"),
        }
    if executable:
        try:
            data = archive.read(f"{prefix}/{executable}")
        except KeyError:
            data = b""
        entry["macho"] = parse_macho(data).as_dict() if data else None
    entry["has_code_signature_dir"] = any(
        name.startswith(prefix + "/_CodeSignature/") for name in archive.namelist()
    )
    entry["embedded_profile"] = prefix + "/embedded.mobileprovision" in archive.namelist()
    if entry["embedded_profile"]:
        profile = profile_entitlements(archive, prefix + "/embedded.mobileprovision")
        if profile:
            entitlements = profile.get("Entitlements", {})
            entry["profile"] = {
                "name": profile.get("Name"),
                "application_identifier": entitlements.get("application-identifier"),
                "team_identifier": entitlements.get("com.apple.developer.team-identifier"),
                "app_groups": entitlements.get("com.apple.security.application-groups"),
                "packet_tunnel": "com.apple.developer.networking.networkextension"
                in entitlements,
                "expires": profile.get("ExpirationDate"),
                "platform": profile.get("Platform"),
            }
    return entry


def audit(path: str) -> dict[str, Any]:
    report: dict[str, Any] = {"path": path}
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        prefix = find_zip_prefix(names)
        if not prefix:
            report["error"] = "no Payload/*.app/Info.plist in the archive"
            return report
        app = describe_bundle(archive, prefix, "app")
        report["app"] = app
        report["extensions"] = [
            describe_bundle(archive, name[: -len("/Info.plist")], "extension")
            for name in names
            if name.startswith(f"{prefix}/PlugIns/")
            and name.endswith(".appex/Info.plist")
        ]
        report["frameworks"] = sorted(
            {
                "/".join(name.split("/")[:5])
                for name in names
                if name.startswith(f"{prefix}/Frameworks/") and name.count("/") >= 5
            }
        )
        report["framework_plists"] = [
            name
            for name in names
            if name.startswith(f"{prefix}/Frameworks/") and name.endswith(".framework/Info.plist")
        ]
        report["app_count"] = len([name for name in names if name.startswith("Payload/") and name.endswith(".app/Info.plist")])
        report["swift_runtime"] = any("/Frameworks/libswiftCore.dylib" in name for name in names)
        report["top_level"] = sorted({name.split("/")[0] for name in names})[:12]
    return report


def summarize(report: dict[str, Any]) -> None:
    app = report.get("app") or {}
    if not app:
        print(f"{report['path']}: {report.get('error', 'unreadable')}")
        return
    info = app.get("info", {})
    macho = app.get("macho") or {}
    print(f"== {report['path']}")
    print(
        f"   app id={info.get('CFBundleIdentifier')} name={info.get('CFBundleName')} "
        f"short={info.get('CFBundleShortVersionString')} build={info.get('CFBundleVersion')}"
    )
    print(
        f"   minOS(plist)={info.get('MinimumOSVersion')} minOS(macho)={macho.get('minos')} "
        f"platform={macho.get('platform')} sdk={macho.get('sdk')} arch={macho.get('arch')}"
    )
    print(
        f"   signed-dir={app.get('has_code_signature_dir')} embedded-profile={app.get('embedded_profile')} "
        f"macho-codesig={macho.get('code_signature')} swift-runtime={report.get('swift_runtime')}"
    )
    for extension in report.get("extensions", []):
        einfo = extension.get("info", {})
        emacho = extension.get("macho") or {}
        point = (extension.get("extension") or {}).get("point")
        print(
            f"     ext {extension['name']}: id={einfo.get('CFBundleIdentifier')} point={point} "
            f"short={einfo.get('CFBundleShortVersionString')} build={einfo.get('CFBundleVersion')} "
            f"minOS={einfo.get('MinimumOSVersion')}/{emacho.get('minos')} "
            f"signed={extension.get('has_code_signature_dir')} profile={extension.get('embedded_profile')}"
        )
        profile = extension.get("profile") or {}
        if profile:
            print(
                f"         profile appid={profile.get('application_identifier')} "
                f"tunnel={profile.get('packet_tunnel')} groups={profile.get('app_groups')}"
            )
    profile = app.get("profile") or {}
    if profile:
        print(
            f"   app profile: appid={profile.get('application_identifier')} "
            f"team={profile.get('team_identifier')} tunnel={profile.get('packet_tunnel')} "
            f"groups={profile.get('app_groups')}"
        )


def main() -> int:
    arguments = sys.argv[1:]
    as_json = False
    if arguments and arguments[0] == "--json":
        as_json = True
        arguments = arguments[1:]
    if not arguments:
        print(__doc__)
        return 2
    reports = [audit(path) for path in arguments]
    if as_json:
        print(json.dumps(reports, indent=2, default=str))
    else:
        for report in reports:
            summarize(report)
            print()
    return 0 if all("error" not in report for report in reports) else 1


if __name__ == "__main__":
    sys.exit(main())
