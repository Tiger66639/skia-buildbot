#!/bin/bash

# this script runs as root inside the chroot environment and updates the depot tools, go environment,
# and skia source.

# need to mount /dev/shm first so that python will execute properly.

mount /dev/shm

SKIA_BUILD=/skia_build
cd ${SKIA_BUILD}

# Install depot_tools.
if [ -d depot_tools ]; then
  (cd depot_tools && git pull);
else
  git clone https://chromium.googlesource.com/chromium/tools/depot_tools.git;
fi
export PATH=$PATH:${SKIA_BUILD}/depot_tools

# Sometimes you need to test patches on the server, to do that uncomment
# the following commented out lines and update the PATCH env variable to the
# name of the codereview to use.

# rm -rf skia

# Checkout the skia code and dependencies.
mkdir skia
cd skia
gclient config --name . https://skia.googlesource.com/skia.git
gclient sync
git checkout master

# PATCH=issue196723021_100001.diff
# rm $PATCH
# wget https://codereview.chromium.org/download/$PATCH
# git apply $PATCH

cp ../fiddle_main/fiddle_secwrap.gyp gyp/

rm -r "${SKIA_BUILD}/skia/out"

GYP_GENERATORS=ninja \
    ./gyp_skia gyp/fiddle_secwrap.gyp gyp/most.gyp \
    -Dskia_mesa=1 -Dskia_no_fontconfig=1

ninja -C ${SKIA_BUILD}/skia/out/Release_Developer \
    skia_lib \
    skgputest \
    pdf \
    libjpeg \
    libjpeg-turbo \
    libSkKTX \
    libetc1 \
    flags \
    sk_tool_utils \
    resources \
    libpng \
    libpng_static \
    libpng_static_neon \
    giflib \
    icuuc \
    sfntly \
    fiddle_secwrap
