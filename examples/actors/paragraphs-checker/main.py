#!/usr/bin/env python3
"""Paragraphs checker for Scraps.

Reads JSON artefacts and checks paragraph content.
Operations: has-paragraphs, has-one-of, paragraph-count.
"""

import json
import os
import sys


def respond(id_, result):
    msg = {"jsonrpc": "2.0", "id": id_, "result": result}
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()


def respond_error(id_, code, message):
    msg = {"jsonrpc": "2.0", "id": id_, "error": {"code": code, "message": message}}
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()


def load_artefact(artefact):
    """Load the paragraphs from the artefact's JSON file."""
    dir_ = artefact["dir"]
    files = artefact["files"]

    json_files = [f for f in files if f.endswith(".json")]
    if not json_files:
        return None, "no JSON file in artefact"

    filepath = os.path.join(dir_, json_files[0])
    if not os.path.exists(filepath):
        return None, f"file not found: {json_files[0]}"

    with open(filepath) as f:
        data = json.load(f)

    if "paragraphs" in data:
        return data["paragraphs"], None
    return None, "no 'paragraphs' key in JSON"


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        req = json.loads(line)
        method = req.get("method")
        params = req.get("params", {})
        id_ = req.get("id")

        if method == "scraps/initialize":
            respond(id_, {"protocol": 1, "outputs": {}})

        elif method == "scraps/shutdown":
            respond(id_, {})
            break

        elif method == "has-paragraphs":
            artefact = params.get("artefact", {})
            texts = params.get("texts", [])
            paragraphs, err = load_artefact(artefact)
            if err:
                respond(id_, {"match": False, "reason": err})
                continue
            missing = [t for t in texts if t not in paragraphs]
            if missing:
                respond(id_, {"match": False, "reason": f"missing paragraphs: {missing}"})
            else:
                respond(id_, {"match": True})

        elif method == "has-one-of":
            artefact = params.get("artefact", {})
            texts = params.get("texts", [])
            paragraphs, err = load_artefact(artefact)
            if err:
                respond(id_, {"match": False, "reason": err})
                continue
            present = [t for t in texts if t in paragraphs]
            if len(present) == 1:
                respond(id_, {"match": True})
            else:
                respond(id_, {"match": False, "reason": f"expected exactly one of {texts}, found {present}"})

        elif method == "paragraph-count":
            artefact = params.get("artefact", {})
            paragraphs, err = load_artefact(artefact)
            if err:
                respond(id_, {"match": False, "reason": err})
                continue
            count = len(paragraphs)
            equals = params.get("equals")
            greater_than = params.get("greater-than")
            if equals is not None and count != equals:
                respond(id_, {"match": False, "reason": f"paragraph count is {count}, expected {equals}"})
            elif greater_than is not None and count <= greater_than:
                respond(id_, {"match": False, "reason": f"paragraph count is {count}, expected > {greater_than}"})
            else:
                respond(id_, {"match": True})

        else:
            respond_error(id_, -32601, f"Unknown method: {method}")


if __name__ == "__main__":
    main()
