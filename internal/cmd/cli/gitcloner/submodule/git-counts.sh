#!/bin/bash

REPO_URL="https://github.com/git-fixtures/basic"
COMMIT_SHA="1669dce138d9b841a518c64b10914d88f5e488ea"

echo "=== Testing against $REPO_URL ==="
echo "=== Target commit: $COMMIT_SHA ==="
echo ""

# -----------------------------------------------------------------------------
# 1. StrategyShallowSHA (depth=1, fetch specific SHA)
# -----------------------------------------------------------------------------
echo "=== StrategyShallowSHA ==="
rm -rf /tmp/shallow-sha-test
mkdir -p /tmp/shallow-sha-test && cd /tmp/shallow-sha-test
git init
git remote add origin $REPO_URL
git fetch --depth 1 origin $COMMIT_SHA
git checkout FETCH_HEAD
echo "ShallowSHA object count: $(git count-objects -v | grep 'count:' | awk '{print $2}')"
echo "ShallowSHA total objects: $(git rev-list --all --objects | wc -l)"
echo ""

# -----------------------------------------------------------------------------
# 2. StrategyFullSHA (fetch specific SHA, no depth limit)
# -----------------------------------------------------------------------------
echo "=== StrategyFullSHA ==="
rm -rf /tmp/full-sha-test
mkdir -p /tmp/full-sha-test && cd /tmp/full-sha-test
git init
git remote add origin $REPO_URL
git fetch origin $COMMIT_SHA
git checkout FETCH_HEAD
echo "FullSHA object count: $(git count-objects -v | grep 'count:' | awk '{print $2}')"
echo "FullSHA total objects: $(git rev-list --all --objects | wc -l)"
echo ""

# -----------------------------------------------------------------------------
# 3. StrategyIncrementalDeepen (shallow clone, then deepen until SHA is reachable)
#    NOTE: This mimics go-git behavior which uses ABSOLUTE depth, not relative --deepen
#    go-git: Depth=1, then Depth=2, then Depth=3... (absolute)
#    git CLI --deepen: adds to existing depth (relative)
#    To simulate go-git, we use --depth=N with increasing N on a FRESH repo each time,
#    or we use --depth=N which sets absolute depth when shallow info exists.
# -----------------------------------------------------------------------------
echo "=== StrategyIncrementalDeepen ==="
rm -rf /tmp/incremental-test
mkdir -p /tmp/incremental-test && cd /tmp/incremental-test
git init
git remote add origin $REPO_URL

echo "Starting incremental deepen loop (go-git style with absolute depth)..."

MAX_ITERATIONS=50
depth=1

while [ $depth -le $MAX_ITERATIONS ]; do
    # Fetch all branches with absolute depth
    # This mimics go-git's: r.FetchContext(ctx, &git.FetchOptions{Depth: depth, ...})
    git fetch --depth=$depth origin 'refs/heads/*:refs/remotes/origin/*' 2>/dev/null

    # Check if commit is reachable
    if git cat-file -e $COMMIT_SHA 2>/dev/null; then
        echo "Commit found at depth $depth"
        break
    fi

    depth=$((depth + 1))
done

if [ $depth -gt $MAX_ITERATIONS ]; then
    echo "WARNING: Commit not found after $MAX_ITERATIONS iterations"
fi

git checkout $COMMIT_SHA 2>/dev/null || echo "Could not checkout commit"
echo "IncrementalDeepen object count: $(git count-objects -v | grep 'count:' | awk '{print $2}')"
echo "IncrementalDeepen total objects: $(git rev-list --all --objects | wc -l)"
echo "IncrementalDeepen final depth: $depth"
echo ""

# -----------------------------------------------------------------------------
# 4. StrategyFullClone (full clone, all branches and tags)
# -----------------------------------------------------------------------------
echo "=== StrategyFullClone ==="
rm -rf /tmp/full-clone-test
git clone $REPO_URL /tmp/full-clone-test
cd /tmp/full-clone-test
git checkout $COMMIT_SHA
echo "FullClone object count: $(git count-objects -v | grep 'count:' | awk '{print $2}')"
echo "FullClone total objects: $(git rev-list --all --objects | wc -l)"
echo ""

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo "=== SUMMARY ==="
echo "Copy these values into expectedObjectCounts map:"
echo ""
echo "var expectedObjectCounts = map[capability.StrategyType]int{"
cd /tmp/shallow-sha-test && echo "    capability.StrategyShallowSHA:        $(git rev-list --all --objects | wc -l | tr -d ' '),"
cd /tmp/full-sha-test && echo "    capability.StrategyFullSHA:           $(git rev-list --all --objects | wc -l | tr -d ' '),"
cd /tmp/incremental-test && echo "    capability.StrategyIncrementalDeepen: $(git rev-list --all --objects | wc -l | tr -d ' '),"
cd /tmp/full-clone-test && echo "    capability.StrategyFullClone:          $(git rev-list --all --objects | wc -l | tr -d ' '),"
echo "}"
