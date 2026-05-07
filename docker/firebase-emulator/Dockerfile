# Firebase emulator image with the Pub/Sub + Firestore emulators
# pre-installed.
#
# firebase-tools >= 14 requires JDK 21+, so we base on eclipse-temurin's
# official noble (Ubuntu 24.04) JRE 21 image and add Node.js 22 on top.
# (Earlier we used node:22-slim + openjdk-17, but firebase-tools dropped
# JDK 17 support — see ADR 0026.)
FROM eclipse-temurin:21-jre-noble

# Node.js 22 from NodeSource (matches the previous node:22-slim baseline)
# plus curl for healthchecks and ca-certificates / gnupg for the apt key.
RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    npm install -g firebase-tools && \
    firebase setup:emulators:pubsub && \
    firebase setup:emulators:firestore && \
    firebase setup:emulators:ui

RUN useradd -m -s /bin/bash emulator && \
    mkdir -p /firebase && chown emulator:emulator /firebase

WORKDIR /firebase
COPY --chown=emulator:emulator firebase.json /firebase/firebase.json

USER emulator

EXPOSE 4000 4400 8080 9399

CMD ["firebase", "emulators:start", "--project=runops-local"]
