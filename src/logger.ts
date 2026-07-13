export type LogLevel = "debug" | "info" | "warn" | "error";

const LEVEL_ORDER: Record<LogLevel, number> = {
    debug: 10,
    info: 20,
    warn: 30,
    error: 40,
};

function resolveThreshold(): number {
    const raw = (globalThis.process.env.LOG_LEVEL ?? "info")
        .trim()
        .toLowerCase();
    return LEVEL_ORDER[raw as LogLevel] ?? LEVEL_ORDER.info;
}

let threshold = resolveThreshold();

export function sanitize(value: unknown, maxLength = 200): string {
    let str =
        typeof value === "string"
            ? value
            : value === undefined
              ? ""
              : String(value);
    // eslint-disable-next-line no-control-regex
    str = str.replace(/[\x00-\x1f\x7f-\x9f]/g, "�");
    if (str.length > maxLength) str = str.slice(0, maxLength) + "…";
    return str;
}

function emit(
    level: LogLevel,
    stream: NodeJS.WriteStream,
    args: unknown[]
): void {
    if (LEVEL_ORDER[level] < threshold) return;
    const ts = new Date().toISOString();
    const line =
        `[${ts}] ${level.toUpperCase().padEnd(5)} ` +
        args
            .map((a) => (typeof a === "string" ? a : JSON.stringify(a)))
            .join(" ");
    stream.write(line + "\n");
}

export const logger = {
    debug: (...args: unknown[]) =>
        emit("debug", globalThis.process.stdout, args),
    info: (...args: unknown[]) => emit("info", globalThis.process.stdout, args),
    warn: (...args: unknown[]) => emit("warn", globalThis.process.stderr, args),
    error: (...args: unknown[]) =>
        emit("error", globalThis.process.stderr, args),
    refresh: () => {
        threshold = resolveThreshold();
    },
    sanitize,
};
