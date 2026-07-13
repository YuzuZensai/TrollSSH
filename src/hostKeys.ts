import fs from "fs";
import path from "path";
import crypto from "crypto";
import sshpk from "sshpk";

function generateAndSave(keyPath: string, type: "rsa" | "ed25519"): void {
    console.log(`Generating ${type} host key...`);

    const { privateKey } =
        type === "rsa"
            ? crypto.generateKeyPairSync("rsa", {
                  modulusLength: 4096,
                  publicKeyEncoding: { type: "pkcs1", format: "pem" },
                  privateKeyEncoding: { type: "pkcs8", format: "pem" },
              })
            : crypto.generateKeyPairSync("ed25519", {
                  publicKeyEncoding: { type: "spki", format: "pem" },
                  privateKeyEncoding: { type: "pkcs8", format: "pem" },
              });

    const parsed = sshpk.parsePrivateKey(privateKey, "pem");
    fs.writeFileSync(keyPath, parsed.toString("openssh"), { mode: 0o600 });
    fs.chmodSync(keyPath, 0o600);
}

export function ensureHostKeys(configDir: string): Buffer[] {
    const keys: Array<{ file: string; type: "rsa" | "ed25519" }> = [
        { file: "id_rsa", type: "rsa" },
        { file: "id_ed25519", type: "ed25519" },
    ];

    return keys.map(({ file, type }) => {
        const keyPath = path.join(configDir, file);
        if (!fs.existsSync(keyPath)) {
            generateAndSave(keyPath, type);
        }
        return fs.readFileSync(keyPath);
    });
}
