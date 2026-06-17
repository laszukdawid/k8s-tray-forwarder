# typed: false
# frozen_string_literal: true

# Build-from-source formula. Fyne is a CGO app, so cross-compiled prebuilt
# binaries (à la GoReleaser) are painful; compiling locally sidesteps that.
class K8sTrayForwarder < Formula
  desc "macOS menu-bar app to toggle Kubernetes port-forwards"
  homepage "https://github.com/laszukdawid/k8s-tray-forwarder"
  license "MIT"
  head "https://github.com/laszukdawid/k8s-tray-forwarder.git", branch: "main"

  # Stable release — uncomment and fill in after tagging a version, e.g.:
  #   git tag v0.1.0 && git push origin v0.1.0
  #   task formula-sha TAG=v0.1.0   # prints the sha256 below
  # url "https://github.com/laszukdawid/k8s-tray-forwarder/archive/refs/tags/v0.1.0.tar.gz"
  # sha256 "REPLACE_WITH_TARBALL_SHA256"
  # version "0.1.0"

  depends_on "go" => :build
  depends_on :macos

  def install
    # Fyne needs CGO + the macOS SDK (Xcode command line tools); both are
    # present in the Homebrew build environment.
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags)
  end

  test do
    assert_match "k8s-tray-forwarder", shell_output("#{bin}/k8s-tray-forwarder --version")
  end
end
