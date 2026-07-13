import fs from "fs";

export interface Config {
    host: string;
    port: number;
    maxLoop: number;
    playbackMode: "loop" | "random";
    allowUserControl: boolean;
    switchDebounceMs: number;
    loginDelay: number;
    maxConnections: number;
    maxTotalConnections: number;
    maxAuthAttempts: number;
    handshakeTimeout: number;
    maxDimension: number;
    frameResolution: number;
    brightnessThreshold: number;
    charset: string;
    invert: boolean;
    logCredentials: boolean;
    videoPath?: string;
}

function parseIntEnv(
    value: string | undefined,
    fallback: number,
    { min, max }: { min?: number; max?: number } = {}
): number {
    if (value === undefined || value.trim() === "") return fallback;
    const parsed = parseInt(value, 10);
    if (!Number.isFinite(parsed)) return fallback;
    let result = parsed;
    if (typeof min === "number") result = Math.max(min, result);
    if (typeof max === "number") result = Math.min(max, result);
    return result;
}

function parseBoolEnv(value: string | undefined): boolean {
    return value === "true";
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
    const videoPath = env.VIDEO_PATH?.trim();

    return {
        host: env.HOST ?? "0.0.0.0",
        port: parseIntEnv(env.PORT, 22, { min: 1, max: 65535 }),
        maxLoop: parseIntEnv(env.MAX_LOOP, 5, { min: 1 }),
        playbackMode:
            env.PLAYBACK_MODE?.trim().toLowerCase() === "random"
                ? "random"
                : "loop",
        allowUserControl: env.ALLOW_USER_CONTROL !== "false",
        switchDebounceMs: parseIntEnv(env.SWITCH_DEBOUNCE_MS, 120, { min: 0 }),
        loginDelay: parseIntEnv(env.LOGIN_DELAY, 1500, { min: 0 }),
        maxConnections: parseIntEnv(env.MAX_CONNECTIONS, 10, { min: 1 }),
        maxTotalConnections: parseIntEnv(env.MAX_TOTAL_CONNECTIONS, 1000, {
            min: 1,
        }),
        maxAuthAttempts: parseIntEnv(env.MAX_AUTH_ATTEMPTS, 6, { min: 1 }),
        handshakeTimeout: parseIntEnv(env.HANDSHAKE_TIMEOUT, 10000, { min: 0 }),
        maxDimension: parseIntEnv(env.MAX_DIMENSION, 512, {
            min: 1,
            max: 4096,
        }),
        frameResolution: parseIntEnv(env.FRAME_RESOLUTION, 360, {
            min: 16,
            max: 1080,
        }),
        brightnessThreshold: parseIntEnv(env.BRIGHTNESS_THRESHOLD, 40, {
            min: 0,
            max: 100,
        }),
        charset: env.CHARSET?.trim() || "detailed",
        invert: parseBoolEnv(env.INVERT),
        logCredentials: parseBoolEnv(env.LOG_CREDENTIALS),
        videoPath: videoPath ? videoPath : undefined,
    };
}

export function loadOptionalTextFile(filePath: string): string | undefined {
    if (!fs.existsSync(filePath)) return undefined;
    return fs.readFileSync(filePath).toString();
}
