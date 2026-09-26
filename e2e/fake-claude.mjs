// A stand-in for the Anthropic Messages API, used by the end-to-end tests so
// the real app can be driven in a browser without a key or network access.
//
// It records every request and answers deterministically:
//   - the title model (haiku) gets a fixed conversation title, or a fixed
//     exercise suggestion when the request asks for structured output;
//   - a user turn that carries an image block gets a reply describing it;
//   - a turn of tool results gets a reply quoting them;
//   - anything else gets an echo of the text it was sent.
// Directives in the user's text script the reply, so tests can drive the
// app's tool-use loop and error handling:
//   <<tool:NAME {json input}>>  reply with a tool_use block (repeatable)
//   <<error STATUS message>>    fail with that HTTP status and API error
//   <<slow MS>>                 wait MS milliseconds before answering
// Test helpers:  GET /_requests  -> recorded requests (JSON array)
//                POST /_reset    -> forget them
//                GET /_health    -> 200 once listening
import http from "node:http";

const port = Number(process.env.FAKE_CLAUDE_PORT || 19911);
let requests = [];

function readJSON(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      } catch (e) {
        reject(e);
      }
    });
    req.on("error", reject);
  });
}

function reply(res, text) {
  send(res, [{ type: "text", text }], "end_turn");
}

function send(res, content, stopReason) {
  const body = {
    id: "msg_fake",
    type: "message",
    role: "assistant",
    model: "fake",
    content,
    stop_reason: stopReason,
    usage: { input_tokens: 1, output_tokens: 1 },
  };
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

function fail(res, status, type, message) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ type: "error", error: { type, message } }));
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The structured-output answer for the custom-exercise "AI" button.
const SUGGESTION = { primary_muscles: ["lats"], secondary_muscles: ["biceps"], equipment: "cable", category: "strength" };

let toolSeq = 0;

// Answers a coach request, following any directives in the last user turn.
async function answer(res, body) {
  const last = body.messages[body.messages.length - 1];
  const parts = Array.isArray(last?.content) ? last.content : [];

  const results = parts.filter((p) => p.type === "tool_result");
  if (results.length) {
    const text = results.map((r) => (r.is_error ? "ERROR " : "") + r.content).join(" | ");
    return reply(res, `Tool results: ${text}`);
  }

  const text = parts.filter((p) => p.type === "text").map((p) => p.text).join(" ");
  const slow = text.match(/<<slow (\d+)>>/);
  if (slow) await sleep(Number(slow[1]));

  const err = text.match(/<<error (\d+) ([^>]*)>>/);
  if (err) return fail(res, Number(err[1]), "api_error", err[2]);

  const calls = [...text.matchAll(/<<tool:(\w+)\s+([\s\S]*?)>>/g)];
  if (calls.length) {
    const offered = new Set((body.tools || []).map((t) => t.name));
    const content = [{ type: "text", text: "On it." }];
    for (const [, name, json] of calls) {
      if (!offered.has(name)) return fail(res, 400, "invalid_request_error", `tool ${name} was not offered`);
      content.push({ type: "tool_use", id: `toolu_${++toolSeq}`, name, input: JSON.parse(json) });
    }
    return send(res, content, "tool_use");
  }
  return reply(res, describe(body));
}

function describe(body) {
  const last = body.messages[body.messages.length - 1];
  const parts = Array.isArray(last?.content) ? last.content : [];
  const images = parts.filter((p) => p.type === "image");
  const text = parts.filter((p) => p.type === "text").map((p) => p.text).join(" ");
  if (images.length) {
    const kinds = images.map((i) => `${i.source.media_type} (${i.source.data.length} b64 chars)`).join(", ");
    return `I can see your photo: that's a **pec deck** machine. [images: ${kinds}] You asked: ${text}`;
  }
  return `No photo attached. You said: ${text}`;
}

const server = http.createServer(async (req, res) => {
  if (req.method === "GET" && req.url === "/_health") {
    res.writeHead(200);
    return res.end("ok");
  }
  if (req.method === "GET" && req.url === "/_requests") {
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify(requests));
  }
  if (req.method === "POST" && req.url === "/_reset") {
    requests = [];
    res.writeHead(204);
    return res.end();
  }
  if (req.method === "POST" && req.url === "/v1/messages") {
    let body;
    try {
      body = await readJSON(req);
    } catch (e) {
      res.writeHead(400, { "Content-Type": "application/json" });
      return res.end(JSON.stringify({ type: "error", error: { type: "invalid_request_error", message: "bad json" } }));
    }
    if (req.headers["x-api-key"] !== "sk-ant-e2e") {
      res.writeHead(401, { "Content-Type": "application/json" });
      return res.end(JSON.stringify({ type: "error", error: { type: "authentication_error", message: "bad key" } }));
    }
    requests.push({
      at: Date.now(),
      model: body.model,
      system: body.system,
      tools: (body.tools || []).map((t) => t.name),
      messages: body.messages,
    });
    if (String(body.model).includes("haiku")) {
      if (body.output_config) return reply(res, JSON.stringify(SUGGESTION));
      return reply(res, "Pec deck question");
    }
    return answer(res, body);
  }
  res.writeHead(404);
  res.end();
});

server.listen(port, "127.0.0.1", () => {
  console.log(`fake claude listening on http://127.0.0.1:${port}`);
});
