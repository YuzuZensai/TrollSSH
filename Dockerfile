FROM oven/bun:1.2-slim
WORKDIR /home/bun/app

RUN apt-get update -y && \
    apt-get install -y ffmpeg && \
    rm -rf /var/lib/apt/lists/*

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production

COPY tsconfig.json ./
COPY src ./src

CMD ["bun", "src/index.ts"]
