import assert from "node:assert/strict";
import test from "node:test";

import {
  normalizeRuntimeSessionID,
  parseRuntimeSessionRoute,
  requireControlAuth,
} from "../src/helpers.js";

test("parseRuntimeSessionRoute parses supported routes", () => {
  const session = parseRuntimeSessionRoute("/internal/runtime/sessions/runtime_123");
  assert.deepEqual(session, { sessionID: "runtime_123", action: "" });

  const input = parseRuntimeSessionRoute("/internal/runtime/sessions/runtime_123/input");
  assert.deepEqual(input, { sessionID: "runtime_123", action: "input" });

  const stream = parseRuntimeSessionRoute("/internal/runtime/sessions/runtime_123/stream");
  assert.deepEqual(stream, { sessionID: "runtime_123", action: "stream" });

  assert.equal(parseRuntimeSessionRoute("/internal/runtime/other"), null);
});

test("normalizeRuntimeSessionID validates expected format", () => {
  assert.equal(normalizeRuntimeSessionID("runtime_abc-123"), "runtime_abc-123");
  assert.equal(normalizeRuntimeSessionID(" runtime_abc "), "runtime_abc");
  assert.equal(normalizeRuntimeSessionID("a"), "");
  assert.equal(normalizeRuntimeSessionID("not valid value"), "");
});

test("requireControlAuth enforces configured service token", async () => {
  const env = {
    CFC_CONTROL_CLIENT_ID: "svc-id",
    CFC_CONTROL_CLIENT_SECRET: "svc-secret",
  };

  const unauthorized = requireControlAuth(new Request("https://example.test/internal/runtime/health"), env);
  assert.ok(unauthorized instanceof Response);
  assert.equal(unauthorized.status, 401);

  const authorized = requireControlAuth(
    new Request("https://example.test/internal/runtime/health", {
      headers: {
        "CF-Access-Client-Id": "svc-id",
        "CF-Access-Client-Secret": "svc-secret",
      },
    }),
    env,
  );
  assert.equal(authorized, null);
});

test("requireControlAuth fails closed when config is missing", () => {
  const result = requireControlAuth(new Request("https://example.test/internal/runtime/health"), {});
  assert.ok(result instanceof Response);
  assert.equal(result.status, 500);
});
