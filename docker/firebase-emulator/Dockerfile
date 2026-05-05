# Firebase emulator image with the Pub/Sub emulator pre-installed.
# Adapted from /Users/nino/ai-code/async/impl/emulator/firebase/Dockerfile.
FROM node:22-slim

RUN npm install -g firebase-tools && \
    firebase setup:emulators:pubsub && \
    firebase setup:emulators:ui && \
    apt-get update && apt-get install -y curl openjdk-17-jre-headless && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash emulator && \
    mkdir -p /firebase && chown emulator:emulator /firebase

WORKDIR /firebase
COPY --chown=emulator:emulator firebase.json /firebase/firebase.json

USER emulator

EXPOSE 4000 4400 9399

CMD ["firebase", "emulators:start", "--project=runops-local"]
