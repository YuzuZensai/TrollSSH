import { describe, test, expect } from "bun:test";
import { loadConfig } from "./config";

describe("loadConfig", () => {
    test("returns defaults when env vars are unset", () => {
        expect(loadConfig({})).toEqual({
            host: "0.0.0.0",
            port: 22,
            maxLoop: 5,
            playbackMode: "loop",
            allowUserControl: true,
            switchDebounceMs: 120,
            loginDelay: 1500,
            maxConnections: 10,
            maxTotalConnections: 1000,
            maxAuthAttempts: 6,
            handshakeTimeout: 10000,
            maxDimension: 512,
            frameResolution: 360,
            brightnessThreshold: 40,
            charset: "detailed",
            invert: false,
            logCredentials: false,
            videoPath: undefined,
        });
    });

    test("overrides defaults from env vars", () => {
        expect(
            loadConfig({
                HOST: "127.0.0.1",
                PORT: "2222",
                MAX_LOOP: "3",
                PLAYBACK_MODE: "random",
                ALLOW_USER_CONTROL: "false",
                SWITCH_DEBOUNCE_MS: "200",
                LOGIN_DELAY: "500",
                MAX_CONNECTIONS: "20",
                MAX_TOTAL_CONNECTIONS: "50",
                MAX_AUTH_ATTEMPTS: "3",
                HANDSHAKE_TIMEOUT: "5000",
                MAX_DIMENSION: "200",
                FRAME_RESOLUTION: "480",
                BRIGHTNESS_THRESHOLD: "60",
                CHARSET: "blocks",
                INVERT: "true",
                LOG_CREDENTIALS: "true",
                VIDEO_PATH: "/videos/clip.mkv",
            })
        ).toEqual({
            host: "127.0.0.1",
            port: 2222,
            maxLoop: 3,
            playbackMode: "random",
            allowUserControl: false,
            switchDebounceMs: 200,
            loginDelay: 500,
            maxConnections: 20,
            maxTotalConnections: 50,
            maxAuthAttempts: 3,
            handshakeTimeout: 5000,
            maxDimension: 200,
            frameResolution: 480,
            brightnessThreshold: 60,
            charset: "blocks",
            invert: true,
            logCredentials: true,
            videoPath: "/videos/clip.mkv",
        });
    });

    test("boolean env vars are true only for the exact string 'true'", () => {
        expect(loadConfig({ LOG_CREDENTIALS: "1" }).logCredentials).toBe(false);
        expect(loadConfig({ INVERT: "yes" }).invert).toBe(false);
        expect(loadConfig({ INVERT: "true" }).invert).toBe(true);
    });

    test("falls back to defaults for non-numeric and out-of-range values", () => {
        expect(loadConfig({ PORT: "not-a-number" }).port).toBe(22);
        expect(loadConfig({ PORT: "99999" }).port).toBe(65535);
        expect(loadConfig({ MAX_DIMENSION: "0" }).maxDimension).toBe(1);
        expect(
            loadConfig({ BRIGHTNESS_THRESHOLD: "999" }).brightnessThreshold
        ).toBe(100);
    });
});
