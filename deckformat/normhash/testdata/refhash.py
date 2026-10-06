#!/usr/bin/env python3
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
"""Independent reference implementation of contract C's normalized hash.

Written from docs/contracts/profile-format.md, not from the Go code, so the
golden value it prints is evidence that the Go implementation matches the
contract rather than merely agreeing with itself. It handles the subset the
fixtures use (no floats; ASCII member names), for which RFC 8785 equals
json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False).

Usage: refhash.py <profile-folder>
"""
import hashlib
import json
import os
import sys
import unicodedata


def jcs(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def walk_strings(obj, fn):
    """Replace every string value (not member names) with fn(value)."""
    if isinstance(obj, dict):
        return {k: walk_strings(v, fn) for k, v in obj.items()}
    if isinstance(obj, list):
        return [walk_strings(v, fn) for v in obj]
    if isinstance(obj, str):
        return fn(obj)
    return obj


def image_ref(images_dir):
    def fn(s):
        if not s.startswith("Images/"):
            return s
        path = os.path.join(images_dir, s[len("Images/"):])
        if not os.path.isfile(path):
            return "missing:" + s
        with open(path, "rb") as f:
            return "sha256:" + hashlib.sha256(f.read()).hexdigest()
    return fn


def main(folder):
    with open(os.path.join(folder, "manifest.json"), encoding="utf-8") as f:
        top = json.load(f)
    pages_dir = os.path.join(folder, "Profiles")
    folders = {name.lower(): name for name in os.listdir(pages_dir)}

    labels = {}
    for i, pid in enumerate(top["Pages"]["Pages"]):
        labels[pid.lower()] = "page/%d" % i
    labels[top["Pages"]["Default"].lower()] = "default"
    for low, name in folders.items():
        labels.setdefault(low, "other/" + name)

    # top-level manifest
    del top["Device"]["UUID"]
    del top["Pages"]["Current"]
    top["Pages"]["Pages"] = [labels[p.lower()] for p in top["Pages"]["Pages"]]
    top["Pages"]["Default"] = labels[top["Pages"]["Default"].lower()]
    top = walk_strings(top, image_ref(os.path.join(folder, "Images")))
    docs = {"manifest.json": top}

    for low, name in folders.items():
        with open(os.path.join(pages_dir, name, "manifest.json"), encoding="utf-8") as f:
            page = json.load(f)
        for controller in page.get("Controllers", []):
            for action in (controller.get("Actions") or {}).values():
                action.pop("State", None)
                action.pop("ActionID", None)
                if "Settings" in action:
                    action["Settings"] = walk_strings(
                        action["Settings"], lambda s: labels.get(s.lower(), s))
        page = walk_strings(page, image_ref(os.path.join(pages_dir, name, "Images")))
        docs[unicodedata.normalize("NFC", labels[low] + "/manifest.json")] = page

    lines = sorted(
        "%s\x00%s\n" % (path, hashlib.sha256(jcs(doc)).hexdigest()) for path, doc in docs.items())
    print(hashlib.sha256("".join(lines).encode("utf-8")).hexdigest())


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: refhash.py <profile-folder>")
    main(sys.argv[1])
