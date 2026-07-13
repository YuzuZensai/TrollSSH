import { describe, test, expect } from "bun:test";
import { loadConfig } from "./config";

describe("loadConfig", () => {
    test("returns defaults when env vars are unset", () => {
        const config = loadConfig({});
        expect(config).toEqual({
            host: "0.0.0.0",
            port: 22,
            maxLoop: 5,
            loginDelay: 1500,
            maxConnections: 10,
            brightnessThreshold: 40,
            logCredentials: false,
        });
    });

    test("overrides defaults from env vars", () => {
        const config = loadConfig({
            HOST: "127.0.0.1",
            PORT: "2222",
            MAX_LOOP: "3",
            LOGIN_DELAY: "500",
            MAX_CONNECTIONS: "20",
            BRIGHTNESS_THRESHOLD: "60",
            LOG_CREDENTIALS: "true",
        });
        expect(config).toEqual({
            host: "127.0.0.1",
            port: 2222,
            maxLoop: 3,
            loginDelay: 500,
            maxConnections: 20,
            brightnessThreshold: 60,
            logCredentials: true,
        });
    });

    test("logCredentials is false for any value other than the string 'true'", () => {
        expect(loadConfig({ LOG_CREDENTIALS: "1" }).logCredentials).toBe(false);
        expect(loadConfig({ LOG_CREDENTIALS: "false" }).logCredentials).toBe(
            false
        );
    });
});
