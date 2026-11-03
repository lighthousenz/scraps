#!/usr/bin/env node
/**
 * Editor actor for Scraps.
 *
 * A Yjs editor client with controllable sync (pause/resume) for building
 * concurrent editing scenarios deterministically.
 *
 * Speaks the Scraps JSON-RPC actor protocol on stdin/stdout.
 */

import { createHash, createHmac } from "node:crypto";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { createInterface } from "node:readline";
import * as Y from "yjs";
import { HocuspocusProvider } from "@hocuspocus/provider";
import WebSocket from "ws";

// --- JSON-RPC helpers ---

function respond(id, result) {
  const msg = JSON.stringify({ jsonrpc: "2.0", id, result });
  process.stdout.write(msg + "\n");
}

function respondError(id, code, message) {
  const msg = JSON.stringify({
    jsonrpc: "2.0",
    id,
    error: { code, message },
  });
  process.stdout.write(msg + "\n");
}

function log(level, message) {
  const msg = JSON.stringify({
    jsonrpc: "2.0",
    method: "scraps/log",
    params: { level, message },
  });
  process.stdout.write(msg + "\n");
}

// --- Helpers ---

/**
 * Derive a deterministic Yjs clientID from the scenario seed and the
 * actor's name so that concurrent insert ordering is reproducible.
 */
function clientIDFromSeed(seed, name) {
  const hash = createHmac("sha256", String(seed)).update(name).digest();
  // Yjs clientIDs are unsigned 32-bit integers.
  return hash.readUInt32BE(0);
}

// --- State ---

let artefactsDir = process.env.SCRAPS_ARTEFACTS || ".";
let serverUrl = null;
let actorSeed = 0;
let actorName = "";
let ydoc = null;
let provider = null;
let synced = false;
let paused = false;

// Buffers for paused mode
let outgoingBuffer = [];
let incomingBuffer = [];

// Original handler references (set after provider is created)
let origDocUpdateHandler = null;
let origOnMessage = null;

// --- Main ---

async function handleRequest(req) {
  const { method, params, id } = req;

  if (method === "scraps/initialize") {
    const inputs = params.inputs || {};
    artefactsDir =
      params.artefacts || process.env.SCRAPS_ARTEFACTS || ".";
    actorSeed = params.seed ?? 0;
    actorName = params.name ?? "";

    // Get the server URL from the actor-typed input
    const serverInput = inputs.server;
    if (serverInput && serverInput.outputs) {
      serverUrl = serverInput.outputs.url;
    }

    respond(id, { protocol: 1, outputs: {} });
  } else if (method === "scraps/shutdown") {
    if (provider) {
      provider.destroy();
      provider = null;
    }
    respond(id, {});
    process.exit(0);
  } else if (method === "connect") {
    if (!serverUrl) {
      respondError(id, 1001, "no server URL available");
      return;
    }

    ydoc = new Y.Doc();
    ydoc.clientID = clientIDFromSeed(actorSeed, actorName);

    provider = new HocuspocusProvider({
      url: serverUrl,
      name: "room",
      document: ydoc,
      WebSocketPolyfill: WebSocket,
      connect: false,
      onSynced() {
        synced = true;
        log("info", "synced with server");
      },
      onDisconnect() {
        synced = false;
      },
    });

    // Save reference to the original onMessage method.
    // HocuspocusProviderWebsocket dispatches incoming messages by
    // calling provider.onMessage(event) on the instance directly,
    // so overriding the instance method intercepts incoming messages.
    origOnMessage = provider.onMessage.bind(provider);
    provider.onMessage = (event) => {
      if (paused) {
        incomingBuffer.push(event);
        return;
      }
      origOnMessage(event);
    };

    // For outgoing updates, the constructor registered
    // boundDocumentUpdateHandler (a captured bind of documentUpdateHandler)
    // as the Yjs doc "update" listener. We need to replace it with our
    // own listener that supports pausing.
    origDocUpdateHandler =
      provider.documentUpdateHandler.bind(provider);
    ydoc.off("update", provider.boundDocumentUpdateHandler);
    const pauseAwareUpdateHandler = (update, origin) => {
      if (paused) {
        outgoingBuffer.push({ update: new Uint8Array(update), origin });
        return;
      }
      origDocUpdateHandler(update, origin);
    };
    ydoc.on("update", pauseAwareUpdateHandler);

    // Now connect with our overrides in place
    await provider.connect();

    // Wait for initial sync
    await new Promise((resolve) => {
      if (synced) {
        resolve();
        return;
      }
      const check = setInterval(() => {
        if (synced) {
          clearInterval(check);
          resolve();
        }
      }, 20);
    });

    respond(id, {});
  } else if (method === "disconnect") {
    if (provider) {
      provider.disconnect();
      synced = false;
    }
    respond(id, {});
  } else if (method === "pause-sync") {
    paused = true;
    outgoingBuffer = [];
    incomingBuffer = [];
    log("info", "sync paused");
    respond(id, {});
  } else if (method === "resume-sync") {
    paused = false;

    // Flush incoming buffer first (apply remote updates)
    for (const event of incomingBuffer) {
      origOnMessage(event);
    }
    incomingBuffer = [];

    // Flush outgoing buffer (send local updates)
    for (const { update, origin } of outgoingBuffer) {
      origDocUpdateHandler(update, origin);
    }
    outgoingBuffer = [];

    log("info", "sync resumed");
    respond(id, {});
  } else if (method === "insert-paragraph") {
    const { at, text } = params;
    const paragraphs = ydoc.getArray("paragraphs");
    if (at === "start") {
      paragraphs.insert(0, [text]);
    } else {
      paragraphs.insert(paragraphs.length, [text]);
    }
    respond(id, {});
  } else if (method === "delete-paragraph") {
    const { index } = params;
    const paragraphs = ydoc.getArray("paragraphs");
    paragraphs.delete(index, 1);
    respond(id, {});
  } else if (method === "status") {
    respond(id, { synced });
  } else if (method === "digest") {
    const paragraphs = ydoc.getArray("paragraphs");
    const arr = paragraphs.toArray();
    const canonical = JSON.stringify(arr);
    const hash = createHash("sha256").update(canonical).digest("hex");
    respond(id, { value: hash });
  } else if (method === "snapshot") {
    const asName = params.as;
    const paragraphs = ydoc.getArray("paragraphs");
    const arr = paragraphs.toArray();
    const filename = `${asName}.json`;
    const filepath = join(artefactsDir, filename);
    writeFileSync(filepath, JSON.stringify({ paragraphs: arr }));
    respond(id, { files: [filename] });
  } else {
    respondError(id, -32601, `Unknown method: ${method}`);
  }
}

// --- Stdin reader ---

const rl = createInterface({ input: process.stdin, terminal: false });

rl.on("line", async (line) => {
  if (!line.trim()) return;
  let reqId;
  try {
    const req = JSON.parse(line);
    reqId = req.id;
    await handleRequest(req);
  } catch (err) {
    process.stderr.write(`editor error: ${err.stack}\n`);
    if (reqId !== undefined) {
      respondError(reqId, 1001, err.message);
    }
  }
});

rl.on("close", () => {
  process.exit(0);
});
