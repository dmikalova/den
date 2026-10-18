This is den's storage array: three data drives pooled with mergerfs, with a
fourth drive holding snapraid parity.

This file exists so the array is never empty. snapraid scrub refuses to run
against an empty array and exits non-zero, which the nightly timer reports as
a failure. Deleting it brings that back.
