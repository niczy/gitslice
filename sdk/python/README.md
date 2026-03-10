# Gitslice Python SDK

Thin Python client for the Gitslice remote filesystem API.

## Install

```bash
cd sdk/python
python3 -m pip install -e .
```

## Usage

```python
from gitslice import GitsliceClient

client = GitsliceClient(base_url="https://agenttools.dev", username="tester")
ws = client.workspace("demo-sdk-workspace")

ws.write("README.md", "hello from python\n")
print(ws.read("README.md"))
print(ws.glob("**/*.md"))

snap = ws.snapshot("initial write")
print(snap.snapshot_id)

with client.workspace("task-123") as task_ws:
    task_ws.write("output.txt", "done\n")
    task_ws.snapshot("task complete")
```

## Notes

- This initial SDK uses the repo's current fake-user auth model via `Authorization: User <username>`.
- Filesystem content is sent using the existing JSON/base64 gateway format.
- `workspace()` will connect to an existing workspace or create it on demand if it does not exist.
- When used as a context manager, a workspace is auto-deleted on exit only if that `workspace()` call created it. Use `cleanup_on_exit=True` to force cleanup.

## Tests

```bash
python3 -m unittest discover -s sdk/python/tests
```
