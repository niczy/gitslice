const SESSION_PATH = /^\/internal\/runtime\/sessions\/([^/]+)(?:\/(input|interrupt|health|stream))?\/?$/;

export function jsonResponse(status, payload, headers = {}) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: {
      "content-type": "application/json",
      "cache-control": "no-store",
      ...headers,
    },
  });
}

export function methodNotAllowed(allow) {
  return jsonResponse(405, { error: "method not allowed" }, { allow });
}

export function notFound() {
  return jsonResponse(404, { error: "not found" });
}

export async function readJSON(request) {
  if (!request) {
    return { ok: false, error: "request is required" };
  }
  try {
    const body = await request.text();
    if (body.trim() === "") {
      return { ok: true, value: {} };
    }
    return { ok: true, value: JSON.parse(body) };
  } catch (_err) {
    return { ok: false, error: "invalid JSON payload" };
  }
}

export function normalizeRuntimeSessionID(candidate) {
  const value = String(candidate || "").trim();
  if (value === "") {
    return `runtime_${crypto.randomUUID()}`;
  }
  if (!/^[a-zA-Z0-9._:-]{3,128}$/.test(value)) {
    return "";
  }
  return value;
}

export function parseRuntimeSessionRoute(pathname) {
  const match = SESSION_PATH.exec(pathname);
  if (!match) {
    return null;
  }
  return {
    sessionID: decodeURIComponent(match[1]),
    action: match[2] || "",
  };
}

export function requireControlAuth(request, env) {
  const expectedID = String(env?.CFC_CONTROL_CLIENT_ID || "").trim();
  const expectedSecret = String(env?.CFC_CONTROL_CLIENT_SECRET || "").trim();
  if (expectedID === "" || expectedSecret === "") {
    return jsonResponse(500, {
      error: "control plane auth is not configured",
      code: "AUTH_CONFIG_MISSING",
    });
  }

  const gotID = String(request.headers.get("CF-Access-Client-Id") || "").trim();
  const gotSecret = String(request.headers.get("CF-Access-Client-Secret") || "").trim();
  if (gotID != expectedID || gotSecret != expectedSecret) {
    return jsonResponse(401, {
      error: "unauthorized",
      code: "AUTH_INVALID",
    });
  }
  return null;
}

export function asSSE(events, latestSeq) {
  const lines = [];
  for (const event of events) {
    lines.push(`id: ${event.seq}`);
    lines.push(`event: ${event.type}`);
    lines.push(`data: ${JSON.stringify(event)}`);
    lines.push("");
  }
  lines.push("event: snapshot_end");
  lines.push(`data: ${JSON.stringify({ latestSeq })}`);
  lines.push("");
  return lines.join("\n");
}
