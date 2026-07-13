import { describe, test, expect } from "bun:test";
import { frameToAscii, resolveCharset, CHARSET_PRESETS } from "./frames";

describe("frameToAscii", () => {
    test("returns empty string for an empty buffer", () => {
        expect(frameToAscii(Buffer.from([]), { brightnessThreshold: 40 })).toBe(
            ""
        );
    });

    test("maps a zero-brightness pixel to the first (blankest) character", () => {
        expect(
            frameToAscii(Buffer.from([0]), { brightnessThreshold: 40 })
        ).toBe(" ");
    });

    test("maps a below-threshold pixel to the first character", () => {
        // brightness = floor((50/255)*100) = 19, below threshold 40
        expect(
            frameToAscii(Buffer.from([50]), { brightnessThreshold: 40 })
        ).toBe(" ");
    });

    test("maps a mid-range pixel to a mid-range character", () => {
        // brightness = floor((128/255)*100) = 50
        expect(
            frameToAscii(Buffer.from([128]), { brightnessThreshold: 40 })
        ).toBe("n");
    });

    test("maps a max-brightness pixel to the last (densest) character", () => {
        expect(
            frameToAscii(Buffer.from([255]), { brightnessThreshold: 40 })
        ).toBe("$");
    });

    test("maps multiple pixels in sequence, preserving order", () => {
        expect(
            frameToAscii(Buffer.from([0, 128, 255]), {
                brightnessThreshold: 40,
            })
        ).toBe(" n$");
    });

    test("uses a named charset preset", () => {
        // standard ramp " .:-=+*#%@": max brightness -> last char "@"
        expect(
            frameToAscii(Buffer.from([255]), {
                brightnessThreshold: 40,
                charset: "standard",
            })
        ).toBe("@");
    });

    test("accepts a custom literal ramp", () => {
        expect(
            frameToAscii(Buffer.from([0, 255]), {
                brightnessThreshold: 0,
                charset: "AB",
            })
        ).toBe("AB");
    });

    test("invert reverses the ramp", () => {
        expect(
            frameToAscii(Buffer.from([255]), {
                brightnessThreshold: 40,
                charset: "AB",
                invert: true,
            })
        ).toBe("A");
    });

    test("supports multi-byte (Unicode) ramps", () => {
        expect(
            frameToAscii(Buffer.from([255]), {
                brightnessThreshold: 40,
                charset: "blocks",
            })
        ).toBe("█");
    });
});

describe("resolveCharset", () => {
    test("returns the detailed preset by default", () => {
        expect(resolveCharset()).toBe(CHARSET_PRESETS.detailed);
    });

    test("resolves preset names case-insensitively", () => {
        expect(resolveCharset("BLOCKS")).toBe(CHARSET_PRESETS.blocks);
    });

    test("passes through a custom ramp", () => {
        expect(resolveCharset(" .#@")).toBe(" .#@");
    });
});
