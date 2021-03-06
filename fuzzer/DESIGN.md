Design of the Skia Fuzzer
=========================

The fuzzer will consist of four mostly independent components:

1. Randomizer - generate, build, and run random standalone skia
   programs.
2. Aggregator - Counts the number of failing programs generated
   by the randomizer, and uploads those metrics to skiamon.
3. Sanitizer - Periodically clean out old, working skia programs
   generated by the randomizer.
4. Web front end - Allow a user to browse all randomized programs,
   quickly find those that are failing, and mark them as fixed.

Randomizer
----------

The randomizer continuously generates standalone Skia programs. It then builds
and runs these programs against the raster, GPU, and PDF backends of skia.
Each such program will be referred to as a "fuzz".

We are not so much interested in the graphical output of a fuzz (although
we will save it), but rather whether the fuzz successfully executed or not.

Each fuzz's source code and generated images / PDF will be stored in Google
storage.  Metadata will also be maintained to record the fuzz's time of
creation, runtime exit status, and system architecture.

Note that the upload to google storage will be optional; users will be able to
run the randomizer as a standalone command-line tool on their desktop for
testing purposes.  In that case, all output will be dumped to a local
directory structure, and metadata will be written to the filesystem as well.

A "good citizen" who runs the fuzzer on their workstation to consume spare
cycles can also upload the results to google storage if their account is
authorized to do so; this is why the system architecture should be recorded in
case we uncover any crashes that are system-specific.

TODO(humper): Discuss the design of the actual random program generation.  You
know, the interesting part :)

Aggregator
----------

The aggregator will periodically count the number of fuzzes in google
storage that have a failing exit status and upload that metric to the skia
monitoring server.  This will allow us to create alerts for failing fuzzes.

The aggregator will run quite frequently, probably at least once a minute.

Sanitizer
---------

Because we don't need to maintain an infinite backlog of working programs, the
sanitizer will periodically purge fuzzes from google storage that are marked
as successful and older than some threshold.

Web Front End
-------------

Skia developers should be able to visually browse the history of the fuzzer and
get quick access to any failing tests.
