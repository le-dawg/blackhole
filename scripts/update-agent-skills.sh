#!/bin/bash
set -e

# update-agent-skills.sh
# Clones standard golang agent skills and copies them to the relevant directories

echo "Cloning cc-skills-golang..."
TEMP_DIR=$(mktemp -d)
git clone https://github.com/samber/cc-skills-golang "$TEMP_DIR"

echo "Copying skills..."
mkdir -p ~/.gemini/config/skills/golang/
mkdir -p .superpowers/skills/golang/

if [ -d "$TEMP_DIR/prompts" ]; then
  cp -r "$TEMP_DIR/prompts/"* ~/.gemini/config/skills/golang/ || true
  cp -r "$TEMP_DIR/prompts/"* .superpowers/skills/golang/ || true
else
  cp -r "$TEMP_DIR/"* ~/.gemini/config/skills/golang/ || true
  cp -r "$TEMP_DIR/"* .superpowers/skills/golang/ || true
fi

rm -rf "$TEMP_DIR"

echo "Agent skills updated."
