import fs from "fs";
import path from "path";
import crypto from "crypto";
import sshpk from "sshpk";

export function ensureHostKeys(configDir: string): void {
    const keyPath = path.join(configDir, "id_rsa");
    if (fs.existsSync(keyPath)) return;

    console.log("Generating host keys...");
    const key = crypto.generateKeyPairSync("rsa", {
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

    fs.writeFileSync(keyPath, keyParsed.toString("openssh"));
}
