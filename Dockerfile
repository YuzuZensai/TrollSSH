FROM oven/bun:1 AS build
WORKDIR /home/bun/app

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

COPY tsconfig.json ./
COPY src ./src
RUN bun run build

FROM oven/bun:1-slim
WORKDIR /home/bun/app

RUN apt-get update -y && \
    apt-get install -y ffmpeg && \
    rm -rf /var/lib/apt/lists/*

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production

COPY --from=build /home/bun/app/dist ./dist

CMD ["bun", "dist/index.js"]
