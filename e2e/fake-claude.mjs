// A stand-in for the Anthropic Messages API, used by the end-to-end tests so
// the real app can be driven in a browser without a key or network access.
//
// It records every request and answers deterministically:
//   - the title model (haiku) gets a fixed conversation title;
//   - a user turn that carries an image block gets a reply describing it;
//   - anything else gets an echo of the text it was sent.
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
  const body = {
    id: "msg_fake",
    type: "message",
    role: "assistant",
    model: "fake",
    content: [{ type: "text", text }],
    stop_reason: "end_turn",
    usage: { input_tokens: 1, output_tokens: 1 },
  };
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
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
    requests.push({ at: Date.now(), model: body.model, system: body.system, messages: body.messages });
    if (String(body.model).includes("haiku")) return reply(res, "Pec deck question");
    return reply(res, describe(body));
  }
  res.writeHead(404);
  res.end();
});

server.listen(port, "127.0.0.1", () => {
  console.log(`fake claude listening on http://127.0.0.1:${port}`);
});
