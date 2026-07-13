import fs from "fs";
import path from "path";
import { loadConfig, loadOptionalTextFile } from "./config";
import { ensureHostKeys } from "./hostKeys";
import { loadFrames } from "./frames";
import { createServer } from "./server";
import videoProcessor from "./videoProcessor";

async function main() {
    const config = loadConfig();
    const configDir = path.join(process.cwd(), "config");

    if (!fs.existsSync(configDir)) {
        fs.mkdirSync(configDir);
    }

    const bannerText = loadOptionalTextFile(path.join(configDir, "banner.txt"));
    const fakeLoginText = loadOptionalTextFile(
        path.join(configDir, "fakelogin.txt")
    );
    const goodbyeText = loadOptionalTextFile(
        path.join(configDir, "goodbye.txt")
    );

    ensureHostKeys(configDir);
    const hostKey = fs.readFileSync(path.join(configDir, "id_rsa"));

    const framesPath = path.join(configDir, "frames.json");
    if (!fs.existsSync(framesPath)) {
        console.error("frames.json not found!, generating frames.json...");
        await videoProcessor.process("video.mp4", framesPath);
    }

    const videoData = loadFrames(framesPath);
    console.log("Loaded frames");

    const server = createServer({
        config,
        hostKey,
        bannerText,
        fakeLoginText,
        goodbyeText,
        videoData,
    });

    server.listen(config.port, config.host);
}

main();
