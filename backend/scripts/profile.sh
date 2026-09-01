#!/bin/bash

BASE_URL="http://localhost:53727"
PROF_DIR="./profiling"
mkdir -p "$PROF_DIR"

show_help() {
    echo "Kanivet Backend Profiling Tool"
    echo ""
    echo "Usage: $0 [command] [options]"
    echo ""
    echo "Commands:"
    echo "  cpu [duration]       - Capture CPU profile (default: 30s)"
    echo "  heap                 - Capture heap memory profile"
    echo "  goroutine            - Capture goroutine profile"
    echo "  allocs               - Capture memory allocation profile"
    echo "  block                - Capture blocking profile"
    echo "  mutex                - Capture mutex contention profile"
    echo "  trace [duration]     - Capture execution trace (default: 5s)"
    echo "  all [duration]       - Capture all profiles (default: 30s)"
    echo "  view [profile]       - View profile in browser"
    echo "  compare [old] [new]  - Compare two profiles"
    echo "  top [profile]        - Show top functions from profile"
    echo "  help                 - Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0 cpu 60                           # 60 second CPU profile"
    echo "  $0 heap                             # Heap snapshot"
    echo "  $0 all                              # All profiles with 30s duration"
    echo "  $0 view profiling/cpu_*.prof        # View CPU profile"
    echo "  $0 top profiling/cpu_*.prof         # Show top CPU consumers"
    echo "  $0 compare old.prof new.prof        # Compare profiles"
}

timestamp() {
    date +%Y%m%d_%H%M%S
}

cpu_profile() {
    local duration=${1:-30}
    local file="$PROF_DIR/cpu_$(timestamp).prof"
    echo "Capturing CPU profile for ${duration}s..."
    curl -s "${BASE_URL}/debug/pprof/profile?seconds=${duration}" -o "$file"
    echo "CPU profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

heap_profile() {
    local file="$PROF_DIR/heap_$(timestamp).prof"
    echo "Capturing heap profile..."
    curl -s "${BASE_URL}/debug/pprof/heap" -o "$file"
    echo "Heap profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

goroutine_profile() {
    local file="$PROF_DIR/goroutine_$(timestamp).prof"
    echo "Capturing goroutine profile..."
    curl -s "${BASE_URL}/debug/pprof/goroutine" -o "$file"
    echo "Goroutine profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

allocs_profile() {
    local file="$PROF_DIR/allocs_$(timestamp).prof"
    echo "Capturing allocations profile..."
    curl -s "${BASE_URL}/debug/pprof/allocs" -o "$file"
    echo "Allocations profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

block_profile() {
    local file="$PROF_DIR/block_$(timestamp).prof"
    echo "Capturing blocking profile..."
    curl -s "${BASE_URL}/debug/pprof/block" -o "$file"
    echo "Block profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

mutex_profile() {
    local file="$PROF_DIR/mutex_$(timestamp).prof"
    echo "Capturing mutex contention profile..."
    curl -s "${BASE_URL}/debug/pprof/mutex" -o "$file"
    echo "Mutex profile saved to: $file"
    echo "View with: go tool pprof $file"
    echo "Or use: $0 view $file"
}

trace_profile() {
    local duration=${1:-5}
    local file="$PROF_DIR/trace_$(timestamp).out"
    echo "Capturing execution trace for ${duration}s..."
    curl -s "${BASE_URL}/debug/pprof/trace?seconds=${duration}" -o "$file"
    echo "Trace saved to: $file"
    echo "View with: go tool trace $file"
}

all_profiles() {
    local duration=${1:-30}
    echo "Capturing all profiles (CPU duration: ${duration}s)..."
    echo ""
    cpu_profile "$duration" &
    local cpu_pid=$!

    heap_profile
    goroutine_profile
    allocs_profile
    block_profile
    mutex_profile

    echo ""
    echo "Waiting for CPU profile to complete..."
    wait $cpu_pid

    echo ""
    echo "All profiles captured in: $PROF_DIR"
    ls -lh "$PROF_DIR"
}

view_profile() {
    local file=$1
    if [ -z "$file" ]; then
        echo "Error: No profile file specified"
        echo "Usage: $0 view <profile-file>"
        return 1
    fi

    if [ ! -f "$file" ]; then
        echo "Error: Profile file not found: $file"
        return 1
    fi

    echo "Opening profile in browser..."
    go tool pprof -http=:8080 "$file"
}

top_functions() {
    local file=$1
    if [ -z "$file" ]; then
        echo "Error: No profile file specified"
        echo "Usage: $0 top <profile-file>"
        return 1
    fi

    if [ ! -f "$file" ]; then
        echo "Error: Profile file not found: $file"
        return 1
    fi

    echo "Top functions from profile:"
    go tool pprof -top -nodecount=20 "$file"
}

compare_profiles() {
    local old=$1
    local new=$2

    if [ -z "$old" ] || [ -z "$new" ]; then
        echo "Error: Two profile files required"
        echo "Usage: $0 compare <old-profile> <new-profile>"
        return 1
    fi

    if [ ! -f "$old" ]; then
        echo "Error: Old profile file not found: $old"
        return 1
    fi

    if [ ! -f "$new" ]; then
        echo "Error: New profile file not found: $new"
        return 1
    fi

    echo "Comparing profiles..."
    go tool pprof -http=:8080 -diff_base="$old" "$new"
}

case "${1:-help}" in
    cpu)
        cpu_profile "$2"
        ;;
    heap)
        heap_profile
        ;;
    goroutine)
        goroutine_profile
        ;;
    allocs)
        allocs_profile
        ;;
    block)
        block_profile
        ;;
    mutex)
        mutex_profile
        ;;
    trace)
        trace_profile "$2"
        ;;
    all)
        all_profiles "$2"
        ;;
    view)
        view_profile "$2"
        ;;
    top)
        top_functions "$2"
        ;;
    compare)
        compare_profiles "$2" "$3"
        ;;
    help|*)
        show_help
        ;;
esac
