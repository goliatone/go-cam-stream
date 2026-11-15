#!/usr/bin/env python3
"""Assemble the native menu-bar app from the same pinned runtime as the proof."""
from pathlib import Path
import runpy

if __name__ == '__main__':
    runpy.run_path(str(Path(__file__).with_name('build-proof.py')))['main'](production=True)
