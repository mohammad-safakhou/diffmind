class Diffmind < Formula
  desc "Deterministic cross-service architecture graphs for developers and agents"
  homepage "https://github.com/mohammad-safakhou/diffmind"
  license "Apache-2.0"
  head "https://github.com/mohammad-safakhou/diffmind.git", branch: "master"

  depends_on "go" => :build
  depends_on "git"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=HEAD"), "./cmd/diffmind"
  end

  test do
    ENV["DIFFMIND_HOME"] = (testpath/"workspace").to_s
    assert_match "diffmind", shell_output("#{bin}/diffmind version")
    assert_match '"ok":true', shell_output("#{bin}/diffmind doctor --json")
    system bin/"diffmind", "backup", "create", "--offline", "--output", testpath/"snapshot.tar.gz"
    system bin/"diffmind", "backup", "verify", "--archive", testpath/"snapshot.tar.gz"
  end
end
