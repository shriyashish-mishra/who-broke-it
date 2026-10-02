#!/usr/bin/env bash
# Print the Homebrew formula for a release. Usage: scripts/formula.sh v0.1.0 > Formula/wbi.rb
set -euo pipefail
V="${1:?usage: formula.sh vX.Y.Z}"; VER="${V#v}"; REPO="shriyashish-mishra/who-broke-it"
SUMS=$(gh release download "$V" -R "$REPO" -p checksums.txt -O - )
sum() { echo "$SUMS" | awk -v f="wbi_${VER}_$1.tar.gz" '$2==f {print $1}'; }
url() { echo "https://github.com/$REPO/releases/download/$V/wbi_${VER}_$1.tar.gz"; }
cat <<RUBY
class Wbi < Formula
  desc "Coordination layer for teams building software with humans and AI coding agents"
  homepage "https://github.com/$REPO"
  version "$VER"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "$(url darwin_arm64)"
      sha256 "$(sum darwin_arm64)"
    else
      url "$(url darwin_amd64)"
      sha256 "$(sum darwin_amd64)"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "$(url linux_arm64)"
      sha256 "$(sum linux_arm64)"
    else
      url "$(url linux_amd64)"
      sha256 "$(sum linux_amd64)"
    end
  end

  def install
    bin.install "wbi"
  end

  test do
    assert_match "$VER", shell_output("#{bin}/wbi version")
  end
end
RUBY
