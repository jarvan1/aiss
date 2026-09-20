# frozen_string_literal: true

# GoReleaser currently emits Homebrew's deprecated `postflight` block for Cask
# install hooks. Rewrite the generated aiss Cask to the restricted install-step
# DSL required by current Homebrew. Keep the replacements deliberately strict so
# a future GoReleaser template change fails the release instead of corrupting the
# Cask silently.

path = ARGV.fetch(0) { abort "usage: #{$PROGRAM_NAME} <cask.rb>" }
source = File.read(path)

replacements = {
  "  postflight do\n" => "  postflight_steps do\n",
  "    if OS.mac?\n" => "    on_macos do\n",
  "      system_command \"/usr/bin/xattr\"" => "      run \"/usr/bin/xattr\"",
  '"#{staged_path}/aiss"' => '"{{staged_path}}/aiss"',
}

replacements.each do |from, to|
  count = source.scan(from).length
  abort "expected exactly one #{from.strip.inspect} in #{path}, found #{count}" unless count == 1

  source = source.sub(from, to)
end

File.write(path, source)
