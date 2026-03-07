#!/usr/bin/env python3
"""
Sample script for keypooler.

Usage by keypooler:
  python3 trigger.py --function=greet --input='{"name":"world"}'

The API key is available via KEYPOOLER_API_KEY env var.
Output must be JSON to stdout: {"success": true, "data": {...}}
"""
import argparse
import json
import os
import sys


def greet(input_data):
    name = input_data.get("name", "world")
    api_key = os.environ.get("KEYPOOLER_API_KEY", "")
    key_preview = api_key[:8] + "..." if len(api_key) > 8 else api_key

    return {
        "success": True,
        "data": {
            "message": f"Hello, {name}!",
            "key_received": bool(api_key),
            "key_preview": key_preview,
        },
    }


def greet_scheduled(input_data):
    return greet(input_data)


FUNCTIONS = {
    "greet": greet,
    "greet_scheduled": greet_scheduled,
}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--function", required=True)
    parser.add_argument("--input", default="{}")
    args = parser.parse_args()

    fn = FUNCTIONS.get(args.function)
    if not fn:
        print(json.dumps({"success": False, "error": f"unknown function: {args.function}"}))
        sys.exit(1)

    try:
        input_data = json.loads(args.input)
    except json.JSONDecodeError as e:
        print(json.dumps({"success": False, "error": f"invalid input JSON: {e}"}))
        sys.exit(1)

    result = fn(input_data)
    print(json.dumps(result))


if __name__ == "__main__":
    main()
