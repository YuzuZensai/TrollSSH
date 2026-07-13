import { describe, test, expect } from "bun:test";
import { ConnectionTracker } from "./server";

describe("ConnectionTracker", () => {
    test("count is 0 for an IP that has never connected", () => {
        const tracker = new ConnectionTracker();
        expect(tracker.count("1.2.3.4")).toBe(0);
    });

    test("increment raises the count for that IP", () => {
        const tracker = new ConnectionTracker();
        tracker.increment("1.2.3.4");
        tracker.increment("1.2.3.4");
        expect(tracker.count("1.2.3.4")).toBe(2);
    });

    test("decrement lowers the count for that IP", () => {
        const tracker = new ConnectionTracker();
        tracker.increment("1.2.3.4");
        tracker.increment("1.2.3.4");
        tracker.decrement("1.2.3.4");
        expect(tracker.count("1.2.3.4")).toBe(1);
    });

    test("decrementing an IP that was never incremented is a no-op", () => {
        const tracker = new ConnectionTracker();
        tracker.decrement("1.2.3.4");
        expect(tracker.count("1.2.3.4")).toBe(0);
    });

    test("hasReachedLimit is true once the count reaches max", () => {
        const tracker = new ConnectionTracker();
        tracker.increment("1.2.3.4");
        tracker.increment("1.2.3.4");
        expect(tracker.hasReachedLimit("1.2.3.4", 2)).toBe(true);
        expect(tracker.hasReachedLimit("1.2.3.4", 3)).toBe(false);
    });

    test("tracks separate counts per IP independently", () => {
        const tracker = new ConnectionTracker();
        tracker.increment("1.1.1.1");
        tracker.increment("2.2.2.2");
        tracker.increment("2.2.2.2");
        expect(tracker.count("1.1.1.1")).toBe(1);
        expect(tracker.count("2.2.2.2")).toBe(2);
    });
});
