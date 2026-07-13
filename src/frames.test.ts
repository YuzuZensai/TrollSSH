import { describe, test, expect } from "bun:test";
import { frameToAscii } from "./frames";

describe("frameToAscii", () => {
    test("returns empty string for an empty buffer", () => {
        expect(frameToAscii(Buffer.from([]), 40)).toBe("");
    });

    test("maps a zero-brightness pixel to the first (blankest) character", () => {
        expect(frameToAscii(Buffer.from([0]), 40)).toBe(" ");
    });

    test("maps a below-threshold pixel to the first character", () => {
        // brightness = floor((50/255)*100) = 19, below threshold 40
        expect(frameToAscii(Buffer.from([50]), 40)).toBe(" ");
    });

    test("maps a mid-range pixel to a mid-range character", () => {
        // brightness = floor((128/255)*100) = 50
        expect(frameToAscii(Buffer.from([128]), 40)).toBe("n");
    });

    test("maps a max-brightness pixel to the last (densest) character", () => {
        // brightness = floor((255/255)*100) = 100, must clamp to last index
        expect(frameToAscii(Buffer.from([255]), 40)).toBe("$");
    });

    test("maps multiple pixels in sequence, preserving order", () => {
        expect(frameToAscii(Buffer.from([0, 128, 255]), 40)).toBe(" n$");
    });
});
