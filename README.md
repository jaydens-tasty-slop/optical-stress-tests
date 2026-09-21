> **NOTE:** This repo is vibecoded but this readme is written by a real human!

# Optical Stress Testing

Some tools for stress testing optical media (CD-R, DVD-R, BD-R).

[See the blog post for why I had a robot build this](https://jayd.ml/2026/09/05/burned-media-torture-test.html)

- Generate ISO files for each file size
- Verification script
- Go utility to generate a sector map for each disc
- Convenience makefile (make sure to set `MEDIA_DIR`)

See [README_LLM.md](./README_LLM.md) for the LLM-slop / more detail.

## Example Sector Map

![Example CD-R sector map](./cdr-sector-map.png)