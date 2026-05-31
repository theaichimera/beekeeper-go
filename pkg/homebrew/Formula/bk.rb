# Reference Homebrew Formula for `bk` (Beekeeper Go).
#
# This file is the SHAPE goreleaser will write into
# theaichimera/homebrew-tap on every stable tag. It's NOT the live
# tap formula — goreleaser owns that path. We keep this here so
# reviewers can see the install surface before any tag goes out.
#
# Once a real release exists:
#   - VERSION, URLs, and SHA256 fields are filled in by goreleaser
#     from the actual built artifacts.
#   - The "VERSION_PLACEHOLDER" / "SHA256_PLACEHOLDER" tokens below
#     are stand-ins for what the live formula will carry.
#
# Verify the live formula after the first release with:
#   brew tap theaichimera/tap
#   brew install bk
#   bk version
#
class Bk < Formula
  desc "Operations layer for beads (bd) — single-binary Go rewrite of beadkeeper"
  homepage "https://github.com/theaichimera/beekeeper-go"
  version "VERSION_PLACEHOLDER"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/theaichimera/beekeeper-go/releases/download/v#{version}/beekeeper-go_#{version}_darwin_arm64.tar.gz"
      sha256 "SHA256_PLACEHOLDER_DARWIN_ARM64"
    else
      url "https://github.com/theaichimera/beekeeper-go/releases/download/v#{version}/beekeeper-go_#{version}_darwin_x86_64.tar.gz"
      sha256 "SHA256_PLACEHOLDER_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/theaichimera/beekeeper-go/releases/download/v#{version}/beekeeper-go_#{version}_linux_arm64.tar.gz"
      sha256 "SHA256_PLACEHOLDER_LINUX_ARM64"
    else
      url "https://github.com/theaichimera/beekeeper-go/releases/download/v#{version}/beekeeper-go_#{version}_linux_x86_64.tar.gz"
      sha256 "SHA256_PLACEHOLDER_LINUX_AMD64"
    end
  end

  def install
    bin.install "bk"
  end

  def caveats
    <<~EOS
      `bk` is the Go port of `beadkeeper` (Python). Both operate on the
      same `.beads/` directory and are interchangeable per-invocation;
      a hook installed by one is recognised by the other (shared marker
      `# beadkeeper-managed: pre-push v1`).

      See:
        https://github.com/theaichimera/beekeeper-go#interop-with-python-beadkeeper
    EOS
  end

  test do
    assert_match "bk", shell_output("#{bin}/bk version")
  end
end
