import {
  asSSE,
  jsonResponse,
  methodNotAllowed,
  notFound,
  normalizeRuntimeSessionID,
  parseRuntimeSessionRoute,
  readJSON,
  requireControlAuth,
} from "./helpers.js";

const runtimeHealthPath = "/internal/runtime/health";
const runtimeSessionsPath = "/internal/runtime/sessions";
const defaultEventBufferSize = 250;

function eventBufferSize(env) {
  const parsed = Number.parseInt(String(env?.CFC_EVENT_BUFFER_SIZE || ""), 10);
  if (Number.isFinite(parsed) && parsed >= 10 && parsed <= 5000) {
    return parsed;
  }
  return defaultEventBufferSize;
}

async function dispatchToSessionDO(env, sessionID, path, init) {
  const binding = env?.RUNTIME_SESSIONS;
  if (!binding) {
    return jsonResponse(500, {
      error: "RUNTIME_SESSIONS durable object binding is missing",
      code: "DO_BINDING_MISSING",
    });
  }
  const id = binding.idFromName(sessionID);
  const stub = binding.get(id);
  try {
    return await stub.fetch(`https://runtime.internal${path}`, init);
  } catch (err) {
    return jsonResponse(502, {
      error: "failed to reach runtime session durable object",
      code: "DO_UNAVAILABLE",
      detail: String(err),
    });
  }
}

async function createRuntimeSession(request, env, url) {
  const decoded = await readJSON(request);
  if (!decoded.ok) {
    return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
  }
  const payload = decoded.value || {};
  const runtimeSessionID = normalizeRuntimeSessionID(payload.runtimeSessionId || payload.sessionId);
  if (runtimeSessionID === "") {
    return jsonResponse(400, {
      error: "sessionId must match [a-zA-Z0-9._:-]{3,128}",
      code: "INVALID_SESSION_ID",
    });
  }

  const doResp = await dispatchToSessionDO(env, runtimeSessionID, "/start", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ ...payload, runtimeSessionId: runtimeSessionID }),
  });
  if (!doResp.ok) {
    return doResp;
  }
  const body = await doResp.json();
  return jsonResponse(201, {
    runtimeSessionId: body.runtimeSessionId || runtimeSessionID,
    status: body.status || "running",
    streamEndpoint: `${url.origin}${runtimeSessionsPath}/${encodeURIComponent(runtimeSessionID)}/stream`,
  });
}

async function routeSessionAction(request, env, match, url) {
  const { sessionID, action } = match;
  switch (action) {
    case "": {
      if (request.method !== "DELETE") {
        return methodNotAllowed("DELETE");
      }
      return dispatchToSessionDO(env, sessionID, "/stop", {
        method: "DELETE",
        headers: { "content-type": "application/json" },
      });
    }
    case "input": {
      if (request.method !== "POST") {
        return methodNotAllowed("POST");
      }
      const decoded = await readJSON(request);
      if (!decoded.ok) {
        return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
      }
      return dispatchToSessionDO(env, sessionID, "/input", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(decoded.value || {}),
      });
    }
    case "interrupt": {
      if (request.method !== "POST") {
        return methodNotAllowed("POST");
      }
      const decoded = await readJSON(request);
      if (!decoded.ok) {
        return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
      }
      return dispatchToSessionDO(env, sessionID, "/interrupt", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(decoded.value || {}),
      });
    }
    case "health": {
      if (request.method !== "GET") {
        return methodNotAllowed("GET");
      }
      return dispatchToSessionDO(env, sessionID, "/health", { method: "GET" });
    }
    case "stream": {
      if (request.method !== "GET") {
        return methodNotAllowed("GET");
      }
      const query = url.search;
      return dispatchToSessionDO(env, sessionID, `/stream${query}`, { method: "GET" });
    }
    default:
      return notFound();
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (!url.pathname.startsWith("/internal/runtime/")) {
      return notFound();
    }

    const authError = requireControlAuth(request, env);
    if (authError) {
      return authError;
    }

    if (url.pathname === runtimeHealthPath) {
      if (request.method !== "GET") {
        return methodNotAllowed("GET");
      }
      return jsonResponse(200, {
        service: "cloudflare-runtime-control",
        status: "healthy",
        durableObjectBinding: Boolean(env?.RUNTIME_SESSIONS),
      });
    }

    if (url.pathname === runtimeSessionsPath) {
      if (request.method !== "POST") {
        return methodNotAllowed("POST");
      }
      return createRuntimeSession(request, env, url);
    }

    const match = parseRuntimeSessionRoute(url.pathname);
    if (!match) {
      return notFound();
    }
    return routeSessionAction(request, env, match, url);
  },
};

export class RuntimeSessionDO {
  constructor(state, env) {
    this.state = state;
    this.env = env;
    this.session = null;
    this.events = [];
    this.seq = 0;
    this.eventBufferSize = eventBufferSize(env);
    this.ready = this.state.blockConcurrencyWhile(async () => {
      const [session, events, seq] = await Promise.all([
        this.state.storage.get("session"),
        this.state.storage.get("events"),
        this.state.storage.get("seq"),
      ]);
      this.session = session || null;
      this.events = Array.isArray(events) ? events : [];
      this.seq = Number.isFinite(seq) ? seq : this.events.reduce((max, event) => Math.max(max, Number(event?.seq) || 0), 0);
    });
  }

  async fetch(request) {
    await this.ready;
    const url = new URL(request.url);

    switch (url.pathname) {
      case "/start":
        if (request.method !== "POST") {
          return methodNotAllowed("POST");
        }
        return this.start(request);
      case "/stop":
        if (request.method !== "DELETE") {
          return methodNotAllowed("DELETE");
        }
        return this.stop(request);
      case "/input":
        if (request.method !== "POST") {
          return methodNotAllowed("POST");
        }
        return this.input(request);
      case "/interrupt":
        if (request.method !== "POST") {
          return methodNotAllowed("POST");
        }
        return this.interrupt(request);
      case "/health":
        if (request.method !== "GET") {
          return methodNotAllowed("GET");
        }
        return this.health();
      case "/stream":
        if (request.method !== "GET") {
          return methodNotAllowed("GET");
        }
        return this.stream(url);
      default:
        return notFound();
    }
  }

  async persist() {
    await this.state.storage.put({
      session: this.session,
      events: this.events,
      seq: this.seq,
    });
  }

  async appendEvent(type, payload) {
    const event = {
      seq: ++this.seq,
      ts: new Date().toISOString(),
      type,
      payload,
    };
    this.events.push(event);
    if (this.events.length > this.eventBufferSize) {
      this.events.splice(0, this.events.length - this.eventBufferSize);
    }
    await this.persist();
    return event;
  }

  async start(request) {
    const decoded = await readJSON(request);
    if (!decoded.ok) {
      return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
    }
    const payload = decoded.value || {};
    const runtimeSessionID = normalizeRuntimeSessionID(payload.runtimeSessionId || payload.sessionId);
    if (runtimeSessionID === "") {
      return jsonResponse(400, {
        error: "runtimeSessionId must match [a-zA-Z0-9._:-]{3,128}",
        code: "INVALID_SESSION_ID",
      });
    }

    if (!this.session) {
      const now = new Date().toISOString();
      this.session = {
        runtimeSessionId: runtimeSessionID,
        profileId: String(payload.profileId || ""),
        sessionId: String(payload.sessionId || payload.runtimeSessionId || ""),
        sliceId: String(payload.sliceId || ""),
        userId: String(payload.userId || ""),
        agentType: String(payload.agentType || ""),
        ttlSec: Number(payload.ttlSec || 0),
        idleTimeoutSec: Number(payload.idleTimeoutSec || 0),
        status: "running",
        createdAt: now,
        updatedAt: now,
      };
      await this.appendEvent("lifecycle.started", {
        runtimeSessionId: runtimeSessionID,
        profileId: this.session.profileId,
      });
      return jsonResponse(201, {
        runtimeSessionId: runtimeSessionID,
        status: this.session.status,
        createdAt: this.session.createdAt,
      });
    }

    this.session.status = "running";
    this.session.updatedAt = new Date().toISOString();
    await this.appendEvent("lifecycle.reused", { runtimeSessionId: this.session.runtimeSessionId });
    return jsonResponse(200, {
      runtimeSessionId: this.session.runtimeSessionId,
      status: this.session.status,
      createdAt: this.session.createdAt,
    });
  }

  async stop(request) {
    if (!this.session) {
      return jsonResponse(200, { status: "stopped" });
    }
    const decoded = await readJSON(request);
    const reason = decoded.ok ? String(decoded.value?.reason || "") : "";

    this.session.status = "stopped";
    this.session.updatedAt = new Date().toISOString();
    this.session.stoppedAt = this.session.updatedAt;
    await this.appendEvent("lifecycle.stopped", {
      runtimeSessionId: this.session.runtimeSessionId,
      reason,
    });
    return jsonResponse(200, {
      runtimeSessionId: this.session.runtimeSessionId,
      status: this.session.status,
      stoppedAt: this.session.stoppedAt,
    });
  }

  async input(request) {
    if (!this.session) {
      return jsonResponse(404, { error: "runtime session not found", code: "SESSION_NOT_FOUND" });
    }
    const decoded = await readJSON(request);
    if (!decoded.ok) {
      return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
    }
    await this.appendEvent("control.input", decoded.value || {});
    this.session.updatedAt = new Date().toISOString();
    await this.persist();
    return jsonResponse(202, { accepted: true, runtimeSessionId: this.session.runtimeSessionId });
  }

  async interrupt(request) {
    if (!this.session) {
      return jsonResponse(404, { error: "runtime session not found", code: "SESSION_NOT_FOUND" });
    }
    const decoded = await readJSON(request);
    if (!decoded.ok) {
      return jsonResponse(400, { error: decoded.error, code: "INVALID_PAYLOAD" });
    }
    await this.appendEvent("control.interrupt", decoded.value || {});
    this.session.updatedAt = new Date().toISOString();
    await this.persist();
    return jsonResponse(202, { accepted: true, runtimeSessionId: this.session.runtimeSessionId });
  }

  health() {
    if (!this.session) {
      return jsonResponse(404, { error: "runtime session not found", code: "SESSION_NOT_FOUND" });
    }
    if (this.session.status === "stopped") {
      return jsonResponse(410, {
        runtimeSessionId: this.session.runtimeSessionId,
        status: this.session.status,
      });
    }
    return jsonResponse(200, {
      runtimeSessionId: this.session.runtimeSessionId,
      status: this.session.status,
      updatedAt: this.session.updatedAt,
    });
  }

  stream(url) {
    if (!this.session) {
      return jsonResponse(404, { error: "runtime session not found", code: "SESSION_NOT_FOUND" });
    }
    const sinceSeq = Number.parseInt(url.searchParams.get("sinceSeq") || "0", 10) || 0;
    const limit = Math.max(1, Math.min(500, Number.parseInt(url.searchParams.get("limit") || "200", 10) || 200));

    const events = this.events
      .filter((event) => Number(event.seq) > sinceSeq)
      .slice(-limit);

    return new Response(asSSE(events, this.seq), {
      status: 200,
      headers: {
        "content-type": "text/event-stream; charset=utf-8",
        "cache-control": "no-store",
        "x-runtime-session-id": this.session.runtimeSessionId,
      },
    });
  }
}
