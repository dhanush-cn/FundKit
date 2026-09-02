#!/bin/bash

SESSION="fundkit"

# Check if tmux is installed
if ! command -v tmux &> /dev/null
then
    echo "tmux could not be found. Please install tmux (e.g., via WSL or Git Bash on Windows) to use this script."
    exit 1
fi

# Kill session if it already exists
tmux kill-session -t $SESSION 2>/dev/null

echo "Starting FundKit in tmux..."

# Create a new detached session with the first window for Infrastructure
tmux new-session -d -s $SESSION -n "Infra"
tmux send-keys -t $SESSION:0 "echo 'Starting Infrastructure...'; docker compose up zookeeper kafka postgres redis prometheus grafana" C-m

# Window 1: Gateway and Frontend
tmux new-window -t $SESSION:1 -n "Gateway-UI"
tmux send-keys -t $SESSION:1 "echo 'Starting API Gateway...'; docker compose up api-gateway" C-m
tmux split-window -h -t $SESSION:1
tmux send-keys -t $SESSION:1 "echo 'Starting Frontend...'; docker compose up frontend" C-m

# Window 2: Backend Microservices
tmux new-window -t $SESSION:2 -n "Microservices"
tmux send-keys -t $SESSION:2 "echo 'Starting Order Service...'; docker compose up order-service" C-m
tmux split-window -h -t $SESSION:2
tmux send-keys -t $SESSION:2 "echo 'Starting Portfolio Service...'; docker compose up portfolio-service" C-m
tmux split-window -v -t $SESSION:2
tmux send-keys -t $SESSION:2 "echo 'Starting Notification Service...'; docker compose up notification-service" C-m

# Select the first window and attach to the session
tmux select-window -t $SESSION:0
tmux attach-session -t $SESSION
