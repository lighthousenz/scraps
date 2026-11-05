# Review guide

When a pull request targets `main` from an `epic/*` branch, the code has already been
through an extensive review process and has landed on the `epic/*` integration branch
through its own pull requests. These promotion pull requests need only a lightweight
review — not a deep look for problems that should already have been addressed — provided
the reviewer confirms:

- Every commit on the epic branch arrived via a reviewed pull request (no direct pushes).
- Merge-conflict resolutions introduced during promotion are correct.

If either condition is not met, review the PR normally.
