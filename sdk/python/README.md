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
home = client.home()

home.write("/tester/README.md", "hello from python\n")
print(home.read("/tester/README.md"))
print(home.glob("/tester/**/*.md"))

snap = home.snapshot("initial write")
print(snap.snapshot_id)
```

## Notes

- This initial SDK uses the repo's current fake-user auth model via `Authorization: User <username>`.
- Filesystem content is sent using the existing JSON/base64 gateway format.
- `home()` resolves the caller's home slice internally and requires absolute paths like `/tester/README.md`.
- The lower-level `workspace()` API is still available for advanced flows that need explicit workspace IDs.

## Tests

```bash
python3 -m unittest discover -s sdk/python/tests
```
