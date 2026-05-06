#!/usr/bin/env python3
"""
MNIST CNN training example for Coach.

Expects MNIST data to be mounted at /data (via Coach).
Writes model checkpoint and metrics to /output/.
"""

import json
import os
import sys
import time

import torch
import torch.nn as nn
import torch.optim as optim
from torch.utils.data import DataLoader
from torchvision import datasets, transforms

# Coach mounts: data/ -> /data, output/ -> /output
DATA_DIR = "/data"
OUTPUT_DIR = "/output"

CONFIG = {
    "epochs": 3,
    "batch_size": 64,
    "lr": 0.001,
    "device": "auto",
}


# --- Model ---
class ConvNet(nn.Module):
    def __init__(self):
        super().__init__()
        self.features = nn.Sequential(
            nn.Conv2d(1, 32, kernel_size=3, padding=1),
            nn.ReLU(),
            nn.MaxPool2d(2),
            nn.Conv2d(32, 64, kernel_size=3, padding=1),
            nn.ReLU(),
            nn.MaxPool2d(2),
        )
        self.classifier = nn.Sequential(
            nn.Flatten(),
            nn.Linear(64 * 7 * 7, 128),
            nn.ReLU(),
            nn.Dropout(0.5),
            nn.Linear(128, 10),
        )

    def forward(self, x):
        x = self.features(x)
        return self.classifier(x)


# --- Training ---
def train_epoch(model, loader, criterion, optimizer, device):
    model.train()
    total_loss = 0.0
    correct = 0
    total = 0

    for data, target in loader:
        data, target = data.to(device), target.to(device)
        optimizer.zero_grad()
        output = model(data)
        loss = criterion(output, target)
        loss.backward()
        optimizer.step()

        total_loss += loss.item()
        pred = output.argmax(dim=1)
        correct += pred.eq(target).sum().item()
        total += target.size(0)

    avg_loss = total_loss / len(loader)
    accuracy = 100.0 * correct / total
    return avg_loss, accuracy


def evaluate(model, loader, criterion, device):
    model.eval()
    total_loss = 0.0
    correct = 0
    total = 0

    with torch.no_grad():
        for data, target in loader:
            data, target = data.to(device), target.to(device)
            output = model(data)
            total_loss += criterion(output, target).item()
            pred = output.argmax(dim=1)
            correct += pred.eq(target).sum().item()
            total += target.size(0)

    avg_loss = total_loss / len(loader)
    accuracy = 100.0 * correct / total
    return avg_loss, accuracy


# --- Main ---
def main():
    os.makedirs(OUTPUT_DIR, exist_ok=True)

    if not os.path.isdir(os.path.join(DATA_DIR, "MNIST", "raw")):
        print(f"ERROR: MNIST data not found at {DATA_DIR}/MNIST/raw", file=sys.stderr)
        sys.exit(1)

    # Resolve device
    device_str = CONFIG["device"]
    if device_str == "auto":
        device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    else:
        device = torch.device(device_str)
    print(f"Using device: {device}")

    transform = transforms.Compose([
        transforms.ToTensor(),
        transforms.Normalize((0.1307,), (0.3081,)),
    ])

    # Coach mounts data/ into /data; download=False because data is already there
    train_dataset = datasets.MNIST(root=DATA_DIR, train=True, download=False, transform=transform)
    test_dataset = datasets.MNIST(root=DATA_DIR, train=False, download=False, transform=transform)

    train_loader = DataLoader(train_dataset, batch_size=CONFIG["batch_size"], shuffle=True)
    test_loader = DataLoader(test_dataset, batch_size=CONFIG["batch_size"], shuffle=False)

    model = ConvNet().to(device)
    criterion = nn.CrossEntropyLoss()
    optimizer = optim.Adam(model.parameters(), lr=CONFIG["lr"])

    metrics = []
    start_time = time.time()

    print(f"\nTraining {CONFIG['epochs']} epochs (batch_size={CONFIG['batch_size']}, lr={CONFIG['lr']}) ...")
    print("-" * 50)

    for epoch in range(1, CONFIG["epochs"] + 1):
        epoch_start = time.time()
        train_loss, train_acc = train_epoch(model, train_loader, criterion, optimizer, device)
        val_loss, val_acc = evaluate(model, test_loader, criterion, device)
        epoch_time = time.time() - epoch_start

        entry = {
            "epoch": epoch,
            "train_loss": round(train_loss, 4),
            "train_acc": round(train_acc, 2),
            "val_loss": round(val_loss, 4),
            "val_acc": round(val_acc, 2),
            "time_sec": round(epoch_time, 2),
        }
        metrics.append(entry)

        print(
            f"Epoch {epoch:>2}/{CONFIG['epochs']} | "
            f"train_loss={train_loss:.4f} train_acc={train_acc:.2f}% | "
            f"val_loss={val_loss:.4f} val_acc={val_acc:.2f}% | "
            f"time={epoch_time:.1f}s"
        )

    total_time = time.time() - start_time

    # Save model with metadata
    model_path = os.path.join(OUTPUT_DIR, "model.pt")
    checkpoint = {
        "state_dict": model.state_dict(),
        "config": CONFIG,
        "architecture": "ConvNet",
        "input_shape": [1, 28, 28],
        "num_classes": 10,
    }
    torch.save(checkpoint, model_path)
    print(f"\nSaved model checkpoint to {model_path}")

    # Save metrics
    metrics_path = os.path.join(OUTPUT_DIR, "metrics.json")
    with open(metrics_path, "w") as f:
        json.dump(
            {
                "config": CONFIG,
                "final_train_acc": metrics[-1]["train_acc"],
                "final_val_acc": metrics[-1]["val_acc"],
                "total_time_sec": round(total_time, 2),
                "epochs": metrics,
            },
            f,
            indent=2,
        )
    print(f"Saved metrics to {metrics_path}")

    print(f"\nDone in {total_time:.1f}s")


if __name__ == "__main__":
    sys.exit(main() or 0)
