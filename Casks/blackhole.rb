cask "blackhole" do
  version "1.0.0"
  sha256 "REPLACE_WITH_SHA256"

  url "https://github.com/blackhole-dns/blackhole/releases/download/v#{version}/blackhole-release.zip"
  name "Blackhole"
  desc "A Pi-hole-grade DNS adblocker for macOS"
  homepage "https://github.com/blackhole-dns/blackhole"

  app "blackhole-release/Blackhole.app"
  binary "blackhole-release/blackhole-dnsd", target: "/usr/local/bin/blackhole-dnsd"

  postflight do
    system_command "xattr",
                   args: ["-rd", "com.apple.quarantine", "#{appdir}/Blackhole.app"],
                   sudo: true
  end

  uninstall delete: "/Library/LaunchDaemons/com.blackhole.dnsd.plist",
            quit:   "com.blackhole.MenuBar"

  caveats <<~EOS
    To start the background DNS daemon, you must run the install script manually:
      sudo $(brew --prefix)/Caskroom/blackhole/#{version}/blackhole-release/install.sh
  EOS
end
