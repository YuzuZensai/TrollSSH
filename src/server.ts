import ssh2 from "ssh2";
import { Config } from "./config";
import { FramesContainer, FrameRenderer } from "./frames";
import { logger, sanitize } from "./logger";

const MAX_WRITE_BACKLOG_BYTES = 4 * 1024 * 1024;

export class ConnectionTracker {
    private counts: Record<string, number> = {};
    private total = 0;

    increment(ip: string): number {
        this.counts[ip] = (this.counts[ip] ?? 0) + 1;
        this.total += 1;
        return this.counts[ip];
    }

    decrement(ip: string): void {
        if (typeof this.counts[ip] === "undefined") return;
        this.counts[ip] -= 1;
        this.total = Math.max(0, this.total - 1);
        if (this.counts[ip] <= 0) delete this.counts[ip];
    }

    count(ip: string): number {
        return this.counts[ip] ?? 0;
    }

    totalCount(): number {
        return this.total;
    }

    hasReachedLimit(ip: string, max: number): boolean {
        return this.count(ip) >= max;
    }

    hasReachedTotalLimit(max: number): boolean {
        return this.total >= max;
    }
}

export interface ServerDeps {
    config: Config;
    hostKeys: Buffer[];
    bannerText?: string;
    fakeLoginText?: string;
    goodbyeText?: string;
    videoSets: FramesContainer[];
}

function clampDimension(value: number, max: number): number {
    if (!Number.isFinite(value) || value < 1) return 1;
    return Math.min(Math.floor(value), max);
}

export function createServer(deps: ServerDeps): ssh2.Server {
    const {
        config,
        hostKeys,
        bannerText,
        fakeLoginText,
        goodbyeText,
        videoSets,
    } = deps;
    const tracker = new ConnectionTracker();

    const sets = videoSets.map((data) => ({
        data,
        renderer: new FrameRenderer(data.frames, {
            brightnessThreshold: config.brightnessThreshold,
            charset: config.charset,
            invert: config.invert,
        }),
    }));

    const server = new ssh2.Server({
        hostKeys,
        banner: bannerText,
    });

    server.on("connection", (client, info) => {
        if (
            tracker.hasReachedTotalLimit(config.maxTotalConnections) ||
            tracker.hasReachedLimit(info.ip, config.maxConnections)
        ) {
            client.on("error", () => {});
            client.end();
            logger.warn("Connection rejected (limit reached) from", info.ip);
            return;
        }

        const activeForIp = tracker.increment(info.ip);
        let currentSetIndex = Math.floor(Math.random() * sets.length);
        let { data: videoData, renderer } = sets[currentSetIndex];

        const pickNextSetIndex = (exclude: number): number => {
            if (sets.length <= 1) return exclude;
            let next = exclude;
            while (next === exclude) {
                next = Math.floor(Math.random() * sets.length);
            }
            return next;
        };

        logger.info(
            `New connection from ${info.ip} ` +
                `(ip=${activeForIp}, total=${tracker.totalCount()}) ` +
                `-> playing "${videoData.name ?? "?"}"`
        );

        let interval: ReturnType<typeof setInterval> | undefined;
        let handshakeTimer: ReturnType<typeof setTimeout> | undefined;
        let authAttempts = 0;
        let ended = false;

        // Force-drop clients that connect but never complete a handshake
        if (config.handshakeTimeout > 0) {
            handshakeTimer = setTimeout(() => {
                logger.warn("Handshake timeout for", info.ip);
                client.end();
            }, config.handshakeTimeout);
        }

        const endSession = () => {
            if (ended) return;
            ended = true;
            tracker.decrement(info.ip);
            if (interval) clearInterval(interval);
            if (handshakeTimer) clearTimeout(handshakeTimer);
        };

        client.on("handshake", () => {
            logger.debug("Handshake from", info.ip);
            if (handshakeTimer) {
                clearTimeout(handshakeTimer);
                handshakeTimer = undefined;
            }
        });

        client.on("close", () => {
            logger.info("Client closed connection from", info.ip);
            endSession();
        });

        client.on("error", (err) => {
            if (err.message === "read ECONNRESET") {
                logger.debug(
                    "Terminal closed (ECONNRESET) for session",
                    info.ip
                );
            } else {
                logger.warn(
                    `Client error from ${info.ip}:`,
                    sanitize(err.message)
                );
            }
            endSession();
        });

        client.on("authentication", (ctx) => {
            if (ctx.method === "password" && config.logCredentials)
                logger.info(
                    `Auth attempt from ${info.ip} method=${ctx.method} ` +
                        `user="${sanitize(ctx.username, 128)}" ` +
                        `pass="${sanitize(ctx.password, 128)}"`
                );

            authAttempts += 1;
            if (authAttempts > config.maxAuthAttempts) {
                client.end();
                return;
            }

            if (ctx.method !== "password") return ctx.reject(["password"]);
            if (!ctx.username) return ctx.reject(["password"]);
            if (!ctx.password) return ctx.reject(["password"]);

            ctx.accept();
        });

        client.on("session", (accept, _reject) => {
            const session = accept();

            let height = clampDimension(24, config.maxDimension);
            let width = clampDimension(80, config.maxDimension);

            session.once("pty", (accept, _reject, data) => {
                logger.debug("Opening pty for session", info.ip);
                height = clampDimension(data.rows, config.maxDimension);
                width = clampDimension(data.cols, config.maxDimension);
                accept();
            });

            session.on("window-change", (_accept, _reject, data) => {
                height = clampDimension(data.rows, config.maxDimension);
                width = clampDimension(data.cols, config.maxDimension);
            });

            const playVideo = (
                stream: ssh2.ServerChannel,
                keepAspectRatio: boolean
            ) => {
                stream.setEncoding("utf8");
                logger.debug(`Terminal size ${width}x${height} for ${info.ip}`);

                if (typeof fakeLoginText !== "undefined") {
                    stream.write("\x1b[2J\x1b[0f");
                    stream.write(fakeLoginText);
                }

                let currentFrame = 0;
                let loopCount = 0;
                let rendering = false;

                const startRenderLoop = () => {
                    interval = setInterval(async () => {
                        if (ended || stream.destroyed) {
                            if (interval) clearInterval(interval);
                            return;
                        }
                        if (rendering) return;
                        if (
                            (stream.writableLength ?? 0) >
                            MAX_WRITE_BACKLOG_BYTES
                        ) {
                            return;
                        }
                        rendering = true;

                        try {
                            const ascii = await renderer.render(
                                currentFrame,
                                width,
                                height,
                                keepAspectRatio
                            );

                            if (ended || stream.destroyed) return;

                            stream.write("\x1b[2J\x1b[0f");
                            stream.write(ascii);

                            currentFrame++;
                            if (currentFrame < videoData.frames.length) {
                                return;
                            }

                            currentFrame = 0;
                            loopCount++;
                            if (loopCount >= config.maxLoop) {
                                if (interval) clearInterval(interval);
                                stream.write("\x1b[2J\x1b[0f");
                                if (typeof goodbyeText !== "undefined") {
                                    stream.write(goodbyeText);
                                }
                                setTimeout(() => {
                                    logger.info(
                                        "Playback finished, closing session",
                                        info.ip
                                    );
                                    stream.end();
                                    client.end();
                                }, 1000);
                                return;
                            }

                            if (config.playbackMode === "random") {
                                currentSetIndex =
                                    pickNextSetIndex(currentSetIndex);
                                ({ data: videoData, renderer } =
                                    sets[currentSetIndex]);
                                logger.info(
                                    `Playthrough done for ${info.ip}, ` +
                                        `switching to "${videoData.name ?? "?"}"`
                                );
                                if (interval) clearInterval(interval);
                                rendering = false;
                                startRenderLoop();
                            } else {
                                logger.info(
                                    `Playthrough done for ${info.ip}, ` +
                                        `looping "${videoData.name ?? "?"}" ` +
                                        `(${loopCount}/${config.maxLoop})`
                                );
                            }
                        } catch (err) {
                            logger.error(
                                "Render error for",
                                info.ip,
                                sanitize(
                                    err instanceof Error ? err.message : err
                                )
                            );
                            if (interval) clearInterval(interval);
                            client.end();
                        } finally {
                            rendering = false;
                        }
                    }, 1000 / videoData.fps);
                };

                let lastSwitch = 0;
                const switchSet = (delta: number) => {
                    if (sets.length <= 1) return;
                    currentSetIndex =
                        (currentSetIndex + delta + sets.length) % sets.length;
                    ({ data: videoData, renderer } = sets[currentSetIndex]);
                    currentFrame = 0;
                    logger.debug(
                        `${info.ip} switched to "${videoData.name ?? "?"}"`
                    );
                    if (interval) {
                        clearInterval(interval);
                        rendering = false;
                        startRenderLoop();
                    }
                };

                if (config.allowUserControl) {
                    stream.on("data", (chunk: Buffer | string) => {
                        const s = chunk.toString();
                        let delta = 0;
                        if (s.includes("\x1b[C") || s.includes("\x1b[A"))
                            delta = 1;
                        else if (s.includes("\x1b[D") || s.includes("\x1b[B"))
                            delta = -1;
                        if (delta === 0) return;
                        const now = Date.now();
                        if (now - lastSwitch < config.switchDebounceMs) return;
                        lastSwitch = now;
                        switchSet(delta);
                    });
                }

                setTimeout(() => {
                    if (ended || stream.destroyed) return;
                    startRenderLoop();
                }, config.loginDelay);
            };

            session.once("exec", (accept, _reject, data) => {
                logger.info(
                    `Client ${info.ip} attempted exec: ` +
                        `"${sanitize(data.command, 512)}"`
                );
                const stream = accept();
                playVideo(stream, false);
            });

            session.once("shell", (accept, _reject) => {
                logger.debug("Opening shell for session", info.ip);
                const stream = accept();
                playVideo(stream, false);
            });
        });
    });

    return server;
}
