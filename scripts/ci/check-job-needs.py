#!/usr/bin/env python3
"""Exits 0 when the named workflow job declares `needs: libbox`.

Reads the workflow on stdin.

Deliberately stdlib-only. The first version imported PyYAML, which is present on a macOS
workstation but NOT on the GitHub runner image, so the check failed there for a reason that had
nothing to do with the workflow it was inspecting.

The job graph is still parsed structurally rather than matched by a line range: a `steps:` list
following the key makes a naive range end in the wrong place, which is how an earlier version
passed while proving nothing. Job blocks are found by indentation, which is what YAML uses.
"""
import re
import sys

job = sys.argv[1]
text = sys.stdin.read()

# A top-level job key sits at exactly two spaces of indentation under `jobs:`.
match = re.search(
    r"^  " + re.escape(job) + r":\n(.*?)(?=^  \S|\Z)",
    text,
    re.M | re.S,
)
if not match:
    sys.exit(2)

block = match.group(1)
# BOTH spellings of `needs:` are accepted, because both are valid YAML and this check is about the
# dependency rather than about which form someone chose:
#
#     needs: libbox                             a scalar
#     needs: [resolve-apple-source, libbox]     a flow sequence
#     needs:                                    a block sequence
#       - resolve-apple-source
#       - libbox
#
# `client-apple.yml` uses the flow sequence, and this check only understood the scalar - it captured
# `[resolve-apple-source,` as the value and exited 1, so it reported "the ios job does not depend on
# libbox" for a job that plainly does.
#
# ORDER MATTERS HERE and that is the subtle part: the scalar pattern `\S+` also matches the first
# token of a sequence, so testing scalar first means the sequence form is never reached and the
# regex that was ADDED to fix this changes nothing. The sequence forms are therefore tested first.
# An earlier attempt at this fix added the sequence branch BELOW the scalar branch and silently did
# nothing, which is the same defect as the one it was fixing: a check that cannot reach its own
# answer.
def declared_needs(text):
    match = re.search(r"^    needs:\s*\[(.*?)\]\s*$", text, re.M)
    if match:
        return [item.strip().strip("'\"") for item in match.group(1).split(",")]

    block_form = re.search(r"^    needs:\s*$\n((?:      -\s*\S+\s*\n)+)", text, re.M)
    if block_form:
        return re.findall(r"-\s*(\S+)", block_form.group(1))

    scalar = re.search(r"^    needs:\s*([^\[\]\s]+)\s*$", text, re.M)
    if scalar:
        return [scalar.group(1)]

    return []


declared = declared_needs(block)

# A refusal must say what it saw. This check reported "the ios job does not depend on libbox" on a
# macOS runner while passing on a Windows workstation with a byte-identical file and the identical
# invocation - and it gave no way to tell the two apart, so each attempt cost a full CI cycle. The
# diagnostic is emitted ONLY on the refusal path, so a passing run prints nothing extra.
if "libbox" not in declared:
    sys.stderr.write(
        "check-job-needs: %r does not declare libbox.\n"
        "  needs lines found in the block: %r\n"
        "  parsed declared needs:          %r\n"
        "  first 3 block lines:            %r\n"
        % (job,
           [l for l in block.splitlines() if "needs:" in l][:4],
           declared,
           block.splitlines()[:3])
    )

sys.exit(0 if "libbox" in declared else 1)
