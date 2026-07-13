import fs from "fs";
import sharp from "sharp";

export interface FramesContainer {
    frames: Buffer[];
    fps: number;
}

const PIXEL_CHARS =
    " .'`^\",:;Il!i><~+_-?][}{1)(|/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$";

export function loadFrames(filename: string): FramesContainer {
    return JSON.parse(fs.readFileSync(filename).toString());
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
        })
        .grayscale()
        .extend({
            top: 0,
            bottom: 0,
            left: 0,
            right: 0,
            background: { r: 0, g: 0, b: 0, alpha: 1 },
        })
        .raw()
        .toBuffer();
}

export function frameToAscii(
    pixels: Buffer,
    brightnessThreshold: number
): string {
    const totalPixelColors = PIXEL_CHARS.length;
    let result = "";

    for (let i = 0; i < pixels.length; i++) {
        const brightness = Math.floor((pixels[i] / 255) * 100);

        let index: number;
        if (brightness < brightnessThreshold) {
            index = 0;
        } else {
            index = Math.min(
                Math.floor((brightness / 100) * totalPixelColors),
                totalPixelColors - 1
            );
        }

        result += PIXEL_CHARS[index];
    }

    return result;
}
