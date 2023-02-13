FROM node:18 as build
WORKDIR /home/node/app

COPY . .

RUN apt-get update -y
RUN apt-get install -y ffmpeg

RUN yarn
RUN yarn build

FROM node:18
WORKDIR /home/node/app

RUN apt-get update -y
RUN apt-get install -y ffmpeg

COPY --from=build /home/node/app/package.json .
COPY --from=build /home/node/app/yarn.lock .

RUN yarn

COPY --from=build /home/node/app/dist .
CMD ["node", "index.js"]