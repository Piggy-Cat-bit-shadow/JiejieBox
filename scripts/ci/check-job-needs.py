#!/usr/bin/env python3
"""Exits 0 when the named workflow job declares `needs: libbox`.

Reads the workflow on stdin.

# Why this is stdlib-only, and why it still parses structure

The first version imported PyYAML, which is present on a macOS workstation but NOT on the GitHub
runner image, so the check failed there for a reason that had nothing to do with the workflow it
was inspecting. It is therefore stdlib-only.

It is *not* therefore a text matcher. The version before this one matched `needs:` with a regular
expression over a block range, and it could not read the workflow it was checking:

  * it understood only the scalar form, so `needs: [resolve-apple-source, libbox]` - which is what
    `client-apple.yml` actually uses - was reported as the job not depending on libbox;
  * once the sequence form was handled it STILL failed on the runner while passing on a workstation
    with a byte-identical file, and it exited 2 rather than 1 - meaning the job block was never
    located at all, so it never even reached the new diagnostic.

Two CI cycles went into that, and both have the same cause: a check that reads YAML with regular
expressions is checking its own assumptions about formatting rather than the file.

So the document is parsed STRUCTURALLY here - enough of YAML's block grammar to walk to `jobs:` and
then to one job's keys - and only the `needs:` value is interpreted. Standard library only, no
assumption about ordering, and the same answer on every platform.

The refusal path prints what it found. A check that cannot say why it refused gets re-run instead of
fixed, which is exactly what happened here twice.
"""
import re
import sys


def strip_comment(line):
    """Remove a trailing comment that is not inside quotes."""
    out = []
    quote = None
    for index, char in enumerate(line):
        if quote:
            if char == quote:
                quote = None
            out.append(char)
            continue
        if char in "'\"":
            quote = char
            out.append(char)
            continue
        if char == "#" and (index == 0 or line[index - 1] in " \t"):
            break
        out.append(char)
    return "".join(out)


def indentation(line):
    return len(line) - len(line.lstrip(" "))


def top_level_section(text, name):
    """The lines of a top-level `<name>:` block, excluding the key line itself."""
    lines = text.splitlines()
    for index, raw in enumerate(lines):
        line = strip_comment(raw).rstrip()
        if not line or indentation(line) != 0:
            continue
        if not line.startswith(name + ":"):
            continue
        body = []
        for following in lines[index + 1:]:
            stripped = strip_comment(following).rstrip()
            if stripped and indentation(stripped) == 0:
                break
            body.append(following)
        return body
    return []


def job_block(jobs_body, job):
    """The lines of one job, excluding its key line. None when the job is absent."""
    for index, raw in enumerate(jobs_body):
        line = strip_comment(raw).rstrip()
        if not line or indentation(line) != 2:
            continue
        key = line.strip()
        if key.rstrip(":") != job:
            continue
        body = []
        for following in jobs_body[index + 1:]:
            stripped = strip_comment(following).rstrip()
            if stripped and indentation(stripped) <= 2:
                break
            body.append(following)
        return body
    return None


def declared_needs(job_body):
    """Every name in the job's `needs:` value, in any of its three spellings."""
    for index, raw in enumerate(job_body):
        line = strip_comment(raw).rstrip()
        match = re.match(r"^(\s*)needs:\s*(.*)$", line)
        if not match:
            continue
        indent, value = len(match.group(1)), match.group(2).strip()

        if value.startswith("[") and value.endswith("]"):
            inner = value[1:-1]
            return [item.strip().strip("'\"") for item in inner.split(",") if item.strip()]

        if value:
            return [value.strip("'\"")]

        # A block sequence under the key.
        names = []
        for following in job_body[index + 1:]:
            entry = strip_comment(following).rstrip()
            if not entry:
                continue
            if indentation(entry) <= indent:
                break
            item = entry.strip()
            if item.startswith("- "):
                names.append(item[2:].strip().strip("'\""))
        return names

    return []


def main():
    if len(sys.argv) != 2:
        sys.stderr.write("usage: check-job-needs.py <job>  (workflow on stdin)\n")
        return 2
    job = sys.argv[1]
    text = sys.stdin.read()

    jobs_body = top_level_section(text, "jobs")
    if not jobs_body:
        sys.stderr.write("check-job-needs: no top-level `jobs:` block found\n")
        return 2

    body = job_block(jobs_body, job)
    if body is None:
        sys.stderr.write(
            "check-job-needs: no job named %r. Jobs found: %r\n"
            % (job,
               [strip_comment(l).strip().rstrip(":")
                for l in jobs_body
                if strip_comment(l).strip() and indentation(strip_comment(l).rstrip()) == 2])
        )
        return 2

    needs = declared_needs(body)
    if "libbox" not in needs:
        sys.stderr.write(
            "check-job-needs: %r does not declare libbox.\n"
            "  needs lines found: %r\n"
            "  parsed needs:      %r\n"
            % (job, [l for l in body if "needs:" in strip_comment(l)][:4], needs)
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
