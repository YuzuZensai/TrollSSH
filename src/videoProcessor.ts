import ffmpeg from "fluent-ffmpeg";
import fs from "fs";
import { FramesContainer } from "./frames";
import { logger } from "./logger";

const SOI = Buffer.from([0xff, 0xd8]);
const EOI = Buffer.from([0xff, 0xd9]);

export interface ProcessOptions {
    maxDimension?: number;
}

class JpegFrameSplitter {
    private buffer = Buffer.alloc(0);

    push(chunk: Buffer): Buffer[] {
        this.buffer = Buffer.concat([this.buffer, chunk]);
        const frames: Buffer[] = [];

        for (;;) {
            const start = this.buffer.indexOf(SOI);
            if (start === -1) break;
            const end = this.buffer.indexOf(EOI, start + SOI.length);
            if (end === -1) break;

            const frameEnd = end + EOI.length;
            frames.push(this.buffer.subarray(start, frameEnd));
            this.buffer = this.buffer.subarray(frameEnd);
        }

        return frames;
    }
}

export async function process(
    path: string,
    output: string,
    options: ProcessOptions = {}
): Promise<void> {
    const maxDimension = options.maxDimension ?? 320;

    return new Promise((resolve, reject) => {
        ffmpeg(path).ffprobe((err, data) => {
            if (err) {
                reject(new Error("ffprobe failed: " + err.message));
                return;
            }

            const stream = data.streams?.[0];
            const rate = stream?.r_frame_rate;
            const fps = rate ? parseFloat(rate) : NaN;
            if (!Number.isFinite(fps) || fps <= 0) {
                reject(new Error("Unable to determine a valid video fps"));
                return;
            }

            const nbFrames = stream?.nb_frames
                ? parseInt(String(stream.nb_frames), 10)
                : NaN;
            const duration = Number(data.format?.duration ?? stream?.duration);
            const totalFrames = Number.isFinite(nbFrames)
                ? nbFrames
                : Number.isFinite(duration)
                  ? Math.round(duration * fps)
                  : undefined;

            const videoData: FramesContainer = { frames: [], fps };
            const splitter = new JpegFrameSplitter();

            let settled = false;
            const fail = (message: string) => {
                if (settled) return;
                settled = true;
                reject(new Error(message));
            };

            const reportProgress = (count: number) => {
                if (totalFrames && totalFrames > 0) {
                    const pct = Math.min(
                        100,
                        Math.round((count / totalFrames) * 100)
                    );
                    globalThis.process.stdout.write(
                        `\rGenerating frames: ${count}/${totalFrames} (${pct}%)`
                    );
                } else {
                    globalThis.process.stdout.write(
                        `\rGenerating frames: ${count}`
                    );
                }
            };

            const ffvideo = ffmpeg(path)
                .outputOptions("-c:v", "mjpeg")
                .outputOptions("-q:v", "3")
                .outputOptions(
                    "-vf",
                    `format=gray,scale=w=${maxDimension}:h=${maxDimension}:` +
                        `force_original_aspect_ratio=decrease`
                )
                .outputOptions("-f", "image2pipe")
                .on("error", (err) => fail("ffmpeg failed: " + err.message));

            const ffstream = ffvideo.pipe();
            ffstream.on("error", (err: Error) =>
                fail("ffmpeg stream error: " + err.message)
            );
            ffstream.on("data", (chunk: Buffer) => {
                for (const frame of splitter.push(chunk)) {
                    videoData.frames.push(frame);
                }
                reportProgress(videoData.frames.length);
            });

            ffstream.on("end", () => {
                if (settled) return;
                if (videoData.frames.length === 0) {
                    fail("No frames were decoded from the video");
                    return;
                }
                settled = true;
                globalThis.process.stdout.write("\n");
                fs.writeFileSync(output, JSON.stringify(videoData));
                logger.info(
                    `Saved ${videoData.frames.length} frames to ${output}`
                );
                resolve();
            });
        });
    });
}

export default {
    process,
};
