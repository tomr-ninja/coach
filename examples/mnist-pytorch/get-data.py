#!/usr/bin/env python3
"""
Download MNIST dataset into the local data/ directory.

Run this BEFORE `coach run` to populate the data folder that Coach will mount
into the container at /data.
"""

import os
from pathlib import Path

import torchvision
from torchvision import transforms

SCRIPT_DIR = Path(__file__).resolve().parent
DATA_DIR = SCRIPT_DIR / "data" / "mnist"


def main():
    os.makedirs(DATA_DIR, exist_ok=True)
    print(f"Downloading MNIST into {DATA_DIR} ...")

    transform = transforms.ToTensor()

    _ = torchvision.datasets.MNIST(
        root=DATA_DIR,
        train=True,
        download=True,
        transform=transform,
    )
    _ = torchvision.datasets.MNIST(
        root=DATA_DIR,
        train=False,
        download=True,
        transform=transform,
    )

    print("Done.")
    print(f"Data is ready at: {DATA_DIR}")
    print("Coach will mount this folder into /data/mnist inside the container.")


if __name__ == "__main__":
    main()
