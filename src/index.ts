import fs from "fs";
import path from "path";
import ssh2 from "ssh2";
import sharp from "sharp";
import crypto from "crypto";
import sshpk from "sshpk";
import dotenv from "dotenv";
import videoProcessor, { FramesContainer } from "./videoProcessor";

dotenv.config();

interface ClientCount {
    [key: string]: number;
}

const pixelColors =
    " .'`^\",:;Il!i><~+_-?][}{1)(|/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$";

function generateHostKeys() {
    let key = crypto.generateKeyPairSync("rsa", {
        modulusLength: 4096,
        publicKeyEncoding: {
            type: "pkcs1",
            format: "pem",
        },
        privateKeyEncoding: {
            type: "pkcs8",
            format: "pem",
        },
    });

    const keyPem = sshpk.parsePrivateKey(key.privateKey, "pem");
    const keyParsed = sshpk.parsePrivateKey(keyPem.toString("pem"));

    fs.writeFileSync(
        path.join(process.cwd(), "config", "id_rsa"),
        keyParsed.toString("openssh")
    );
}

async function resizeFrame(
    frame: Buffer,
    width: number,
    height: number,
    keep_aspect_ratio = false
) {
    const resized_frame = await sharp(frame)
        .resize(width, height, {
            fit: keep_aspect_ratio ? "contain" : "fill",
        })
        .grayscale()
        .extend({
            top: 0,
            bottom: 0,
            left: 0,
            right: 0,
            background: { r: 0, g: 0, b: 0, alpha: 1 },
        })
        .raw()
        .toBuffer();
    return resized_frame;
}

function loadFrames(filename: string) {
    const frames: FramesContainer = JSON.parse(
        fs.readFileSync(filename).toString()
    );
    return frames;
}

async function printFrameASCII(
    stream: ssh2.WriteStream,
    frame: Buffer,
    width: number,
    height: number,
    brightness_threshold: number,
    keep_aspect_ratio = false
) {
    const resized_frame = await resizeFrame(
        frame,
        width,
        height,
        keep_aspect_ratio
    );

    let frame_string = "";
    for (let i = 0; i < resized_frame.length; i++) {
        const pixel = resized_frame[i];

        const brightness = Math.floor((pixel / 255) * 100);
        let color;
        let totalPixelColors = pixelColors.length;
        let choosenPixelColors;
        if (brightness > 100) {
            choosenPixelColors = totalPixelColors - 1;
        } else if (brightness < brightness_threshold) {
            choosenPixelColors = 0;
        } else {
            choosenPixelColors = Math.floor(
                (brightness / 100) * totalPixelColors
            );
        }
        color = pixelColors[choosenPixelColors];

        frame_string += color;
    }

    stream.write("ok\x1Bc[0G");

    stream.write("\x1b[2J\x1b[0f");
    stream.write(frame_string);
}

async function main() {
    const HOST = process.env.HOST ? process.env.HOST : "0.0.0.0";
    const PORT = process.env.PORT ? parseInt(process.env.PORT) : 22;
    const MAX_LOOP = process.env.MAX_LOOP ? parseInt(process.env.MAX_LOOP) : 5;
    const LOGIN_DELAY = process.env.LOGIN_DELAY
        ? parseInt(process.env.LOGIN_DELAY)
        : 1500;
    const MAX_CONNECTIONS = process.env.MAX_CONNECTIONS
        ? parseInt(process.env.MAX_CONNECTIONS)
        : 10;
    const BRIGHTNESS_THRESHOLD = process.env.BRIGHTNESS_THRESHOLD ? parseInt(process.env.BRIGHTNESS_THRESHOLD) : 40;

    if (!fs.existsSync(path.join(process.cwd(), "config"))) {
        fs.mkdirSync(path.join(process.cwd(), "config"));
    }

    let bannerText: string | undefined;
    if (fs.existsSync(path.join(process.cwd(), "config", "banner.txt"))) {
        bannerText = fs
            .readFileSync(path.join(process.cwd(), "config", "banner.txt"))
            .toString();
    }

    let fakeLoginText: string | undefined;
    if (fs.existsSync(path.join(process.cwd(), "config", "fakelogin.txt"))) {
        fakeLoginText = fs
            .readFileSync(path.join(process.cwd(), "config", "fakelogin.txt"))
            .toString();
    }

    let goodbyeText: string | undefined;
    if (fs.existsSync(path.join(process.cwd(), "config", "goodbye.txt"))) {
        goodbyeText = fs
            .readFileSync(path.join(process.cwd(), "config", "goodbye.txt"))
            .toString();
    }

    if (!fs.existsSync(path.join(process.cwd(), "config", "id_rsa"))) {
        console.log("Generating host keys...");
        generateHostKeys();
    }

    if (!fs.existsSync(path.join(process.cwd(), "config", "frames.json"))) {
        console.error("frames.json not found!, generating frames.json...");
        await videoProcessor.process("video.mp4", "config/frames.json");
    }

    const videoData = loadFrames("config/frames.json");
    console.log("Loaded frames");

    const server = new ssh2.Server({
        hostKeys: [
            fs.readFileSync(path.join(process.cwd(), "config", "id_rsa")),
        ],
        banner: bannerText,
    });

    let clientCount: ClientCount = {};

    server.on("connection", (client, info) => {
        if (clientCount[info.ip] >= MAX_CONNECTIONS) {
            client.on("error", (err) => {});
            client.end();
            return;
        }

        clientCount[info.ip] = clientCount[info.ip]
            ? clientCount[info.ip] + 1
            : 1;

        console.log("New connection from", info.ip);
        client.on("handshake", () => {
            console.log("Handshake from", info.ip);
        });

        client.on("close", () => {
            console.log("Client closed connection from", info.ip);
            if (typeof clientCount[info.ip] !== "undefined")
                clientCount[info.ip] = clientCount[info.ip] - 1;
            else delete clientCount[info.ip];
            if (interval) clearInterval(interval);
        });

        let interval: NodeJS.Timer | undefined;

        client.on("error", (err) => {
            if (err.message === "read ECONNRESET") {
                console.log(
                    "Terminal closed (ECONNRESET) for session",
                    info.ip
                );
                if (typeof clientCount[info.ip] !== "undefined")
                    clientCount[info.ip] = clientCount[info.ip] - 1;
                else delete clientCount[info.ip];

                if (interval) clearInterval(interval);
                return;
            }
            console.log("Client error: ", err);
        });

        client.on("authentication", (ctx) => {
            if (!ctx.username) return ctx.reject(["password"]);
            if (ctx.method != "password") return ctx.reject(["password"]);
            if (!ctx.password) return ctx.reject(["password"]);

            ctx.accept();
        });

        client.on("session", (accept, reject) => {
            const session = accept();

            let height = 100;
            let width = 100;

            session.once("pty", (accept, reject, data) => {
                console.log("Opening pty for session", info.ip);
                height = data.rows;
                width = data.cols;
                accept();
            });

            session.on("window-change", (accept, reject, data) => {
                // console.log("Terminal resized for session", info.ip);
                height = data.rows;
                width = data.cols;
            });

            const playVideo = (stream: any, keep_aspect_ratio: boolean) => {
                stream.setEncoding("utf8");

                console.log("Terminal size: " + width + "x" + height);

                if (typeof fakeLoginText !== "undefined") {
                    stream.write("\x1b[2J\x1b[0f");
                    stream.write(fakeLoginText);
                }

                setTimeout(() => {
                    let current_frame = 0;
                    let loop_count = 0;

                    interval = setInterval(async () => {
                        if (stream.destroyed) {
                            clearInterval(interval);
                            return;
                        }

                        const frame = Buffer.from(
                            videoData.frames[current_frame]
                        );
                        await printFrameASCII(
                            stream,
                            frame,
                            width,
                            height,
                            BRIGHTNESS_THRESHOLD,
                            keep_aspect_ratio
                        );
                        current_frame++;
                        if (current_frame >= videoData.frames.length) {
                            current_frame = 0;
                            loop_count++;
                            if (loop_count >= MAX_LOOP) {
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
                                    if (
                                        typeof clientCount[info.ip] !==
                                        "undefined"
                                    )
                                        clientCount[info.ip] =
                                            clientCount[info.ip] - 1;
                                    else delete clientCount[info.ip];
                                    stream.end();
                                    client.end();
                                }, 1000);
                            }
                        }
                    }, 1000 / videoData.fps);
                }, LOGIN_DELAY);
            };

            session.once("exec", (accept, reject, data) => {
                console.log(
                    "Client",
                    info.ip,
                    "is trying to execute command",
                    '"' + data.command + '"'
                );
                const stream = accept();
                playVideo(stream, false);
            });

            session.once("shell", (accept, reject) => {
                console.log("Opening shell for session", info.ip);
                const stream = accept();
                playVideo(stream, false);
            });
        });
    });

    server.listen(PORT, HOST);
}
main();
