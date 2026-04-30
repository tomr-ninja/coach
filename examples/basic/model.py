import os
import sys


def main():
    data_dir = "/data"
    output_dir = "/output"

    chunks = sorted(
        f for f in os.listdir(data_dir)
        if os.path.isfile(os.path.join(data_dir, f))
    )

    os.makedirs(output_dir, exist_ok=True)

    words = []
    for c in chunks:
        with open(os.path.join(data_dir, c)) as cf:
            words.extend(cf.read().split())
    words.sort()

    output_path = os.path.join(output_dir, "sorted_chunks.txt")
    with open(output_path, "w") as f:
        f.write(" ".join(words))

    print(f"Wrote {len(words)} sorted words to {output_path}")


if __name__ == "__main__":
    main()
