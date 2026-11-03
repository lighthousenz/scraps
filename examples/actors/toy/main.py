#!/usr/bin/env python3
"""Toy actor for Scraps verdict testing.

Behaviour depends on the toy subject's major version:
  1 - correct
  2 - write stores an empty string
  3 - write stores the text upper-cased
  4 - write returns error 1000
  5 - prints a non-JSON line to stdout on write (protocol violation)
  6 - the ready condition never holds
"""

import json
import os
import sys


def respond(id_, result):
    msg = {"jsonrpc": "2.0", "id": id_, "result": result}
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()


def respond_error(id_, code, message, data=None):
    error = {"code": code, "message": message}
    if data is not None:
        error["data"] = data
    msg = {"jsonrpc": "2.0", "id": id_, "error": error}
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()


def main():
    stored_text = ""
    major = None
    artefacts_dir = None
    ready = False

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        req = json.loads(line)
        method = req.get("method")
        params = req.get("params", {})
        id_ = req.get("id")

        if method == "scraps/initialize":
            subjects = params.get("subjects", {})
            toy_version = subjects.get("toy", "1.0.0")
            parts = toy_version.split(".")
            major = int(parts[0])
            artefacts_dir = params.get("artefacts", os.environ.get("SCRAPS_ARTEFACTS", "."))

            # Major 6: ready condition never holds
            if major != 6:
                ready = True

            respond(id_, {"protocol": 1, "outputs": {}})

        elif method == "scraps/shutdown":
            respond(id_, {})
            break

        elif method == "status":
            respond(id_, {"ready": ready})

        elif method == "write":
            text = params.get("text", "")

            if major == 2:
                stored_text = ""
                respond(id_, {})
            elif major == 3:
                stored_text = text.upper()
                respond(id_, {})
            elif major == 4:
                respond_error(id_, 1000, "toy write failed")
            elif major == 5:
                # Protocol violation: print a non-JSON line to stdout
                sys.stdout.write("THIS IS NOT JSON\n")
                sys.stdout.flush()
                respond(id_, {})
            else:
                # major 1 (correct) and any other
                stored_text = text
                respond(id_, {})

        elif method == "snapshot":
            as_name = params.get("as", "snapshot")
            filename = f"{as_name}.json"
            filepath = os.path.join(artefacts_dir, filename)
            content = json.dumps({"text": stored_text})
            with open(filepath, "w") as f:
                f.write(content)
            respond(id_, {"files": [filename]})

        else:
            respond_error(id_, -32601, f"Unknown method: {method}")


if __name__ == "__main__":
    main()
