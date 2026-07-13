import ssh2 from "ssh2";
import { Config } from "./config";
import { FramesContainer, resizeFrame, frameToAscii } from "./frames";

export class ConnectionTracker {
    private counts: Record<string, number> = {};

    increment(ip: string): number {
        this.counts[ip] = (this.counts[ip] ?? 0) + 1;
        return this.counts[ip];
    }

    decrement(ip: string): void {
        if (typeof this.counts[ip] === "undefined") return;
        this.counts[ip] -= 1;
        if (this.counts[ip] <= 0) delete this.counts[ip];
    }

    count(ip: string): number {
        return this.counts[ip] ?? 0;
    }

    hasReachedLimit(ip: string, max: number): boolean {
        return this.count(ip) >= max;
    }
}

export interface ServerDeps {
    config: Config;
    hostKey: Buffer;
    bannerText?: string;
    fakeLoginText?: string;
    goodbyeText?: string;
    videoData: FramesContainer;
}

export function createServer(deps: ServerDeps): ssh2.Server {
    const {
        config,
        hostKey,
        bannerText,
        fakeLoginText,
        goodbyeText,
        videoData,
    } = deps;
    const tracker = new ConnectionTracker();

    const server = new ssh2.Server({
        hostKeys: [hostKey],
        banner: bannerText,
    });

    server.on("connection", (client, info) => {
        if (tracker.hasReachedLimit(info.ip, config.maxConnections)) {
            client.on("error", () => {});
            client.end();
            return;
        }

        tracker.increment(info.ip);
        console.log("New connection from", info.ip);

        client.on("handshake", () => {
            console.log("Handshake from", info.ip);
        });

        let interval: ReturnType<typeof setInterval> | undefined;

        const endSession = () => {
            tracker.decrement(info.ip);
            if (interval) clearInterval(interval);
        };

        client.on("close", () => {
            console.log("Client closed connection from", info.ip);
            endSession();
        });

        client.on("error", (err) => {
            if (err.message === "read ECONNRESET") {
                console.log(
                    "Terminal closed (ECONNRESET) for session",
                    info.ip
                );
                endSession();
                return;
            }
            console.log("Client error: ", err);
        });

        client.on("authentication", (ctx) => {
            if (ctx.method === "password" && config.logCredentials)
                console.log(
                    "Authentication from",
                    info.ip,
                    ctx.method,
                    ctx.username,
                    ctx.password
                );
            if (!ctx.username) return ctx.reject(["password"]);
            if (ctx.method !== "password") return ctx.reject(["password"]);
            if (!ctx.password) return ctx.reject(["password"]);

            ctx.accept();
        });

        client.on("session", (accept, _reject) => {
            const session = accept();

            let height = 100;
            let width = 100;

            session.once("pty", (accept, _reject, data) => {
                console.log("Opening pty for session", info.ip);
                height = data.rows;
                width = data.cols;
                accept();
            });

            session.on("window-change", (_accept, _reject, data) => {
                height = data.rows;
                width = data.cols;
            });

            const playVideo = (
                stream: ssh2.ServerChannel,
                keepAspectRatio: boolean
            ) => {
                stream.setEncoding("utf8");
                console.log("Terminal size: " + width + "x" + height);

                if (typeof fakeLoginText !== "undefined") {
                    stream.write("\x1b[2J\x1b[0f");
                    stream.write(fakeLoginText);
                }

                setTimeout(() => {
                    let currentFrame = 0;
                    let loopCount = 0;

                    interval = setInterval(async () => {
                        if (stream.destroyed) {
                            clearInterval(interval);
                            return;
                        }

                        const frame = Buffer.from(
                            videoData.frames[currentFrame]
                        );
                        const resized = await resizeFrame(
                            frame,
                            width,
                            height,
                            keepAspectRatio
                        );
                        const ascii = frameToAscii(
                            resized,
                            config.brightnessThreshold
                        );

                        stream.write("ok\x1Bc[0G");
                        stream.write("\x1b[2J\x1b[0f");
                        stream.write(ascii);

                        currentFrame++;
                        if (currentFrame >= videoData.frames.length) {
                            currentFrame = 0;
                            loopCount++;
                            if (loopCount >= config.maxLoop) {
                                clearInterval(interval);
                                stream.write("\x1b[2J\x1b[0f");
                                if (typeof goodbyeText !== "undefined") {
                                    stream.write(goodbyeText);
                                }
                                setTimeout(() => {
                                    console.log(
                                        "Terminal closed for session",
                                        info.ip
                                    );
                                    tracker.decrement(info.ip);
                                    stream.end();
                                    client.end();
                                }, 1000);
                            }
                        }
                    }, 1000 / videoData.fps);
                }, config.loginDelay);
            };

            session.once("exec", (accept, _reject, data) => {
                console.log(
                    "Client",
                    info.ip,
                    "is trying to execute command",
                    '"' + data.command + '"'
                );
                const stream = accept();
                playVideo(stream, false);
            });

            session.once("shell", (accept, _reject) => {
                console.log("Opening shell for session", info.ip);
                const stream = accept();
                playVideo(stream, false);
            });
        });
    });

    return server;
}
