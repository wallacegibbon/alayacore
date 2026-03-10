#!/bin/bash
# cc-commit.sh - CoreClaw git commit wrapper
# Automatically appends CoreClaw attribution to commit messages
#
# Usage:
#   cc-commit.sh "commit message"
#   cc-commit.sh -m "commit message"
#   echo "commit message" | cc-commit.sh -
#
# This script wraps git commit to always include CoreClaw attribution.
# It does NOT modify .git hooks or affect other users.

set -e

ATTRIBUTION="

Generated with CoreClaw"

# Parse arguments
MESSAGE=""
EXTRA_ARGS=()
GIT_ARGS=()

while [[ $# -gt 0 ]]; do
	case $1 in
		-m)
			shift
			if [[ -z "$1" ]]; then
				echo "Error: -m requires a message argument" >&2
				exit 1
			fi
			MESSAGE="$1"
			shift
			;;
		--amend)
			# Pass through --amend without adding attribution
			GIT_ARGS+=("--amend")
			shift
			;;
		--no-edit)
			GIT_ARGS+=("--no-edit")
			shift
			;;
		-C|-c|--fixup|--squash)
			# These options take a commit reference
			GIT_ARGS+=("$1")
			shift
			if [[ -n "$1" ]]; then
				GIT_ARGS+=("$1")
				shift
			fi
			;;
		-a|--all|-p|--patch|-i|--interactive|-v|--verbose|-q|--quiet|--dry-run|--short|--branch|-s|-S|--gpg-sign)
			# Pass through common git commit flags
			GIT_ARGS+=("$1")
			shift
			;;
		-*)
			# Unknown flag, pass to git
			GIT_ARGS+=("$1")
			shift
			;;
		*)
			# Non-flag argument: treat as message if we don't have one yet
			if [[ -z "$MESSAGE" ]]; then
				MESSAGE="$1"
			else
				EXTRA_ARGS+=("$1")
			fi
			shift
			;;
	esac
done

# Check if we're in a git repository
if ! git rev-parse --is-inside-work-tree &>/dev/null; then
	echo "Error: Not a git repository" >&2
	exit 1
fi

# Check for amend mode - don't add attribution when amending
if [[ " ${GIT_ARGS[*]} " =~ " --amend " ]]; then
	# Auto-add --no-edit to prevent interactive editor from spawning
	if [[ ! " ${GIT_ARGS[*]} " =~ " --no-edit " ]]; then
		GIT_ARGS+=("--no-edit")
	fi
	exec git commit "${GIT_ARGS[@]}" "${EXTRA_ARGS[@]}"
fi

# If no message provided, error out to prevent interactive editor
if [[ -z "$MESSAGE" ]]; then
	echo "Error: No commit message provided" >&2
	echo "Usage: cc-commit.sh \"your message\" or cc-commit.sh -m \"your message\"" >&2
	echo "Interactive editor is disabled to prevent hanging in non-interactive environments" >&2
	exit 1
fi

# Check if message already contains attribution
if [[ "$MESSAGE" == *"Generated with CoreClaw"* ]]; then
	# Already has attribution, commit as-is
	git commit -m "$MESSAGE" "${GIT_ARGS[@]}" "${EXTRA_ARGS[@]}"
else
	# Append attribution and commit
	git commit -m "$MESSAGE$ATTRIBUTION" "${GIT_ARGS[@]}" "${EXTRA_ARGS[@]}"
fi
