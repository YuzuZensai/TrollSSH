import { parentPort, workerData } from "worker_threads";
import fs from "fs";

function loadPacked(filename: string): {
    fps: number;
    lengths: Uint32Array;
    packed: Uint8Array;
} {
    const parsed = JSON.parse(fs.readFileSync(filename, "utf8"));

    if (
        !parsed ||
        !Array.isArray(parsed.frames) ||
        parsed.frames.length === 0 ||
        typeof parsed.fps !== "number" ||
        !Number.isFinite(parsed.fps) ||
        parsed.fps <= 0
    ) {
        throw new Error(
            `Invalid frames file "${filename}": expected non-empty frames[] and a positive fps`
        );
    }

    const raw = parsed.frames as unknown[];
    const count = raw.length;
    const lengths = new Uint32Array(count);
    const bufs: Buffer[] = new Array(count);
    let total = 0;
    for (let i = 0; i < count; i++) {
        const b = Buffer.from(raw[i] as Uint8Array);
        bufs[i] = b;
        lengths[i] = b.length;
        total += b.length;
    }

    const packed = new Uint8Array(total);
    let off = 0;
    for (let i = 0; i < count; i++) {
        packed.set(bufs[i], off);
        off += lengths[i];
    }

    return { fps: parsed.fps, lengths, packed };
}

const { filename } = workerData as { filename: string };
const { fps, lengths, packed } = loadPacked(filename);
parentPort!.postMessage(
    { fps, lengths: lengths.buffer, packed: packed.buffer },
    [lengths.buffer, packed.buffer] as never
);
