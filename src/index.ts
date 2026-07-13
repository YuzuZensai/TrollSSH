import fs from "fs";
import os from "os";
import path from "path";
import { loadConfig, loadOptionalTextFile, Config } from "./config";
import { ensureHostKeys } from "./hostKeys";
import { loadFramesAsync, FramesContainer } from "./frames";
import { createServer } from "./server";
import videoProcessor from "./videoProcessor";
import { logger } from "./logger";

const DATA_DIR = path.join(process.cwd(), "data");
const FRAMES_DIR = path.join(process.cwd(), "frames");

function fail(message: string): never {
    logger.error(message);
    process.exit(1);
}

interface Args {
    generate: boolean;
    video?: string;
}

function parseArgs(argv: string[]): Args {
    const args: Args = { generate: false };
    for (let i = 0; i < argv.length; i++) {
        const arg = argv[i];
        if (arg === "--generate" || arg === "-g") args.generate = true;
        else if (arg === "--video" || arg === "-v") args.video = argv[++i];
    }
    return args;
}

function resolveVideoPath(explicitPath?: string): string | undefined {
    if (explicitPath) {
        const explicit = path.resolve(explicitPath);
        return fs.existsSync(explicit) ? explicit : undefined;
    }

    const cwd = process.cwd();
    const match = fs
        .readdirSync(cwd)
        .filter((name) => path.parse(name).name.toLowerCase() === "video")
        .sort()
        .find((name) => fs.statSync(path.join(cwd, name)).isFile());

    return match ? path.join(cwd, match) : undefined;
}

async function generateFrames(
    config: Config,
    videoArg?: string
): Promise<void> {
    const videoPath = resolveVideoPath(videoArg ?? config.videoPath);
    if (!videoPath) {
        fail(
            `No source video found. Pass --video <path>, set VIDEO_PATH, or ` +
                `drop a "video.*" file in "${process.cwd()}".`
        );
    }

    fs.mkdirSync(FRAMES_DIR, { recursive: true });
    const output = path.join(FRAMES_DIR, `${path.parse(videoPath).name}.json`);

    logger.info(`Generating frames from "${videoPath}" -> ${output}`);
    try {
        await videoProcessor.process(videoPath, output, {
            maxDimension: config.frameResolution,
        });
    } catch (err) {
        fail(
            `Failed to generate frames from "${videoPath}": ` +
                (err instanceof Error ? err.message : String(err))
        );
    }
}

async function loadAllFrames(): Promise<FramesContainer[]> {
    const files = fs.existsSync(FRAMES_DIR)
        ? fs
              .readdirSync(FRAMES_DIR)
              .filter((name) => name.toLowerCase().endsWith(".json"))
              .sort()
        : [];

    if (files.length === 0) {
        fail(
            `No frame sets found in "${FRAMES_DIR}". ` +
                `Generate one first with: bun src/index.ts --generate --video <path>`
        );
    }

    const cpus = os.availableParallelism?.() ?? os.cpus().length;
    const concurrency = Math.min(files.length, Math.max(1, Math.min(cpus, 4)));

    const results: FramesContainer[] = new Array(files.length);
    let nextIndex = 0;
    const worker = async () => {
        for (let i = nextIndex++; i < files.length; i = nextIndex++) {
            const file = files[i];
            const filePath = path.join(FRAMES_DIR, file);
            const sizeMb = (fs.statSync(filePath).size / 1024 / 1024).toFixed(
                1
            );
            logger.info(`Loading ${file} (${sizeMb} MB)...`);
            const data = await loadFramesAsync(filePath);
            data.name = file;
            logger.info(
                `  ${file}: ${data.frames.length} frames @ ${data.fps}fps`
            );
            results[i] = data;
        }
    };

    await Promise.all(Array.from({ length: concurrency }, () => worker()));
    return results;
}

async function main() {
    const config = loadConfig();
    const args = parseArgs(process.argv.slice(2));

    if (args.generate) {
        await generateFrames(config, args.video);
        return;
    }

    fs.mkdirSync(DATA_DIR, { recursive: true });

    const bannerText = loadOptionalTextFile(path.join(DATA_DIR, "banner.txt"));
    const fakeLoginText = loadOptionalTextFile(
        path.join(DATA_DIR, "fakelogin.txt")
    );
    const goodbyeText = loadOptionalTextFile(
        path.join(DATA_DIR, "goodbye.txt")
    );

    const hostKeys = ensureHostKeys(DATA_DIR);
    const videoSets = await loadAllFrames();
    logger.info(`Loaded ${videoSets.length} frame set(s)`);

    const server = createServer({
        config,
        hostKeys,
        bannerText,
        fakeLoginText,
        goodbyeText,
        videoSets,
    });

    server.on("error", (err: Error) => {
        logger.error("Server error:", err.message);
        process.exit(1);
    });

    server.listen(config.port, config.host, () => {
        logger.info(`TrollSSH listening on ${config.host}:${config.port}`);
    });

    const shutdown = (signal: string) => {
        logger.info(`Received ${signal}, shutting down...`);
        server.close(() => process.exit(0));
        // Fail-safe: force exit if connections don't drain promptly.
        setTimeout(() => process.exit(0), 5000).unref();
    };
    process.on("SIGINT", () => shutdown("SIGINT"));
    process.on("SIGTERM", () => shutdown("SIGTERM"));
}

main().catch((err) => {
    fail(err instanceof Error ? err.message : String(err));
});
