import ffmpeg from "fluent-ffmpeg";
import fs from "fs";

export interface FramesContainer {
    frames: Buffer[];
    fps: number;
}

export async function process(path: string, output: string): Promise<void> {
    return new Promise((resolve, reject) => {
        ffmpeg(path).ffprobe((err, data) => {
            if (err) {
                throw new Error("An error occurred: " + err.message);
            }

            if (!data.streams[0].r_frame_rate)
                throw new Error("Unable to get video fps");

            const videoData: FramesContainer = {
                frames: [],
                fps: parseFloat(data.streams[0].r_frame_rate),
            };

            const ffvideo = ffmpeg(path)
                .outputOptions("-f image2pipe")
                .outputOptions("-vf format=gray")
                .on("error", (err) => {
                    console.log("An error occurred: " + err.message);
                });

            const ffstream = ffvideo.pipe();
            ffstream.on("data", (chunk) => {
                videoData.frames.push(chunk);
            });

            ffstream.on("end", () => {
                fs.writeFileSync(output, JSON.stringify(videoData));
                console.log("Frames saved to", output);
                resolve();
            });
        });
    });
}

export default {
    process
};
