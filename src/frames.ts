import fs from "fs";
import path from "path";
import { Worker } from "worker_threads";
import sharp from "sharp";

const WORKER_PATH = path.join(__dirname, "frameLoader.worker.ts");

export interface FramesContainer {
    frames: Buffer[];
    fps: number;
    name?: string;
}

export const CHARSET_PRESETS: Record<string, string> = {
    detailed:
        " .'`^\",:;Il!i><~+_-?][}{1)(|/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$",
    standard: " .:-=+*#%@",
    simple: " .:oO#@",
    blocks: " ░▒▓█",
};

const DEFAULT_CHARSET = CHARSET_PRESETS.detailed;

export interface AsciiOptions {
    brightnessThreshold?: number;
    charset?: string;
    invert?: boolean;
}

export function resolveCharset(charset?: string): string {
    if (!charset) return DEFAULT_CHARSET;
    return CHARSET_PRESETS[charset.toLowerCase()] ?? charset;
}

export function loadFrames(filename: string): FramesContainer {
    const parsed = JSON.parse(fs.readFileSync(filename).toString());

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

    return parsed as FramesContainer;
}

export function loadFramesAsync(filename: string): Promise<FramesContainer> {
    return new Promise((resolve, reject) => {
        const worker = new Worker(WORKER_PATH, { workerData: { filename } });
        worker.once(
            "message",
            (msg: {
                fps: number;
                lengths: ArrayBuffer;
                packed: ArrayBuffer;
            }) => {
                const lengths = new Uint32Array(msg.lengths);
                const frames: Buffer[] = new Array(lengths.length);
                let off = 0;
                for (let i = 0; i < lengths.length; i++) {
                    frames[i] = Buffer.from(msg.packed, off, lengths[i]);
                    off += lengths[i];
                }
                resolve({ frames, fps: msg.fps });
                worker.terminate();
            }
        );
        worker.once("error", (err) => {
            worker.terminate();
            reject(err);
        });
    });
}

export async function resizeFrame(
    frame: Buffer,
    width: number,
    height: number,
    keepAspectRatio = false
): Promise<Buffer> {
    return sharp(frame)
        .resize(width, height, {
            fit: keepAspectRatio ? "contain" : "fill",
            background: { r: 0, g: 0, b: 0, alpha: 1 },
        })
        .grayscale()
        .removeAlpha()
        .raw()
        .toBuffer();
}

export class FrameRenderer {
    private cache = new Map<string, string>();

    constructor(
        private readonly frames: Buffer[],
        private readonly options: AsciiOptions,
        private readonly maxEntries = 4096
    ) {}

    async render(
        index: number,
        width: number,
        height: number,
        keepAspectRatio: boolean
    ): Promise<string> {
        const key = `${index}:${width}x${height}:${keepAspectRatio ? 1 : 0}`;
        const cached = this.cache.get(key);
        if (cached !== undefined) {
            this.cache.delete(key);
            this.cache.set(key, cached);
            return cached;
        }

        const source = Buffer.isBuffer(this.frames[index])
            ? this.frames[index]
            : Buffer.from(this.frames[index] as unknown as Uint8Array);
        const resized = await resizeFrame(
            source,
            width,
            height,
            keepAspectRatio
        );
        const ascii = frameToAscii(resized, this.options);

        this.cache.set(key, ascii);
        if (this.cache.size > this.maxEntries) {
            const oldest = this.cache.keys().next().value;
            if (oldest !== undefined) this.cache.delete(oldest);
        }
        return ascii;
    }
}

export function frameToAscii(
    pixels: Buffer,
    options: AsciiOptions = {}
): string {
    const { brightnessThreshold = 40, charset, invert = false } = options;

    const ramp = [...resolveCharset(charset)];
    const total = ramp.length;
    let result = "";

    for (let i = 0; i < pixels.length; i++) {
        const brightness = Math.floor((pixels[i] / 255) * 100);

        let index: number;
        if (brightness < brightnessThreshold) {
            index = 0;
        } else {
            index = Math.min(Math.floor((brightness / 100) * total), total - 1);
        }

        if (invert) index = total - 1 - index;
        result += ramp[index];
    }

    return result;
}
