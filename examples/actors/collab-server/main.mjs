#!/usr/bin/env node
/**
 * Collab-server actor for Scraps.
 *
 * Runs a Hocuspocus WebSocket server that serves one Yjs room.
 * Speaks the Scraps JSON-RPC actor protocol on stdin/stdout.
 */

import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { createInterface } from "node:readline";
import * as Y from "yjs";
import { Hocuspocus } from "@hocuspocus/server";

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

// --- State ---

let workdir = process.env.SCRAPS_WORKDIR || ".";
let documentPath = null;
let server = null;
let listening = false;
let port = 0;
let seedDoc = null; // holds the initial/current state

// --- Main ---

async function handleRequest(req) {
  const { method, params, id } = req;

  if (method === "scraps/initialize") {
    const inputs = params.inputs || {};
    workdir = params.workdir || process.env.SCRAPS_WORKDIR || ".";

    // The document input is a path to a fixture directory containing paragraphs.json
    documentPath = inputs.document;

    // Build the initial Yjs state
    seedDoc = new Y.Doc();

    const roomBinPath = join(workdir, "room.bin");
    if (existsSync(roomBinPath)) {
      const data = readFileSync(roomBinPath);
      Y.applyUpdate(seedDoc, new Uint8Array(data));
      log("info", "loaded room state from room.bin");
    } else if (documentPath) {
      let paragraphsPath;
      if (documentPath.startsWith("/")) {
        paragraphsPath = join(documentPath, "paragraphs.json");
      } else {
        paragraphsPath = join(process.cwd(), documentPath, "paragraphs.json");
      }
      if (existsSync(paragraphsPath)) {
        const paragraphs = JSON.parse(readFileSync(paragraphsPath, "utf-8"));
        const arr = seedDoc.getArray("paragraphs");
        arr.insert(0, paragraphs);
        log("info", `seeded ${paragraphs.length} paragraphs from fixture`);
      }
    }

    // Set up Hocuspocus with onLoadDocument to seed initial state
    server = new Hocuspocus({
      address: "127.0.0.1",
      port: 0,
      quiet: true,
      async onLoadDocument({ document }) {
        if (seedDoc) {
          const update = Y.encodeStateAsUpdate(seedDoc);
          Y.applyUpdate(document, update);
        }
        return document;
      },
      async onStoreDocument({ document }) {
        // Keep seedDoc in sync so save-state writes latest
        seedDoc = new Y.Doc();
        const update = Y.encodeStateAsUpdate(document);
        Y.applyUpdate(seedDoc, update);
      },
    });

    await new Promise((resolve) => {
      server.listen(() => {
        port = server.address.port;
        listening = true;
        log("info", `listening on ws://127.0.0.1:${port}`);
        resolve();
      });
    });

    respond(id, {
      protocol: 1,
      outputs: { url: `ws://127.0.0.1:${port}` },
    });
  } else if (method === "scraps/shutdown") {
    await saveState();

    if (server) {
      await server.destroy();
    }
    listening = false;
    respond(id, {});
    process.exit(0);
  } else if (method === "status") {
    respond(id, { listening });
  } else if (method === "save-state") {
    await saveState();
    respond(id, {});
  } else {
    respondError(id, -32601, `Unknown method: ${method}`);
  }
}

async function saveState() {
  if (seedDoc && workdir) {
    const state = Y.encodeStateAsUpdate(seedDoc);
    const roomBinPath = join(workdir, "room.bin");
    writeFileSync(roomBinPath, Buffer.from(state));
    log("info", "saved room state to room.bin");
  }
}

// --- Stdin reader ---

const rl = createInterface({ input: process.stdin, terminal: false });

rl.on("line", async (line) => {
  if (!line.trim()) return;
  try {
    const req = JSON.parse(line);
    await handleRequest(req);
  } catch (err) {
    process.stderr.write(`collab-server error: ${err.message}\n`);
  }
});

rl.on("close", () => {
  process.exit(0);
});
