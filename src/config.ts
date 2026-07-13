import fs from "fs";

export interface Config {
    host: string;
    port: number;
    maxLoop: number;
    loginDelay: number;
    maxConnections: number;
    brightnessThreshold: number;
    logCredentials: boolean;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
    return {
        host: env.HOST ?? "0.0.0.0",
        port: env.PORT ? parseInt(env.PORT, 10) : 22,
        maxLoop: env.MAX_LOOP ? parseInt(env.MAX_LOOP, 10) : 5,
        loginDelay: env.LOGIN_DELAY ? parseInt(env.LOGIN_DELAY, 10) : 1500,
        maxConnections: env.MAX_CONNECTIONS
            ? parseInt(env.MAX_CONNECTIONS, 10)
            : 10,
        brightnessThreshold: env.BRIGHTNESS_THRESHOLD
            ? parseInt(env.BRIGHTNESS_THRESHOLD, 10)
            : 40,
        logCredentials: env.LOG_CREDENTIALS === "true",
    };
}

export function loadOptionalTextFile(filePath: string): string | undefined {
    if (!fs.existsSync(filePath)) return undefined;
    return fs.readFileSync(filePath).toString();
}
