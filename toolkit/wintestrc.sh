#!/bin/bash

set -e

echo -e "\033[33m=== compiling ===\033[0m"

GOOS=windows go test -c -v -o __debug_build.exe "$@"

echo -e "\033[34m=== compiled ===\033[0m"

scp __debug_build.exe ${WINDOWS_ADDRESS}:"C:\Users\administrator\Desktop\run.exe"

echo -e "\033[32m=== file uploaded ===\033[0m"

set +e

ssh ${WINDOWS_ADDRESS} "%USERPROFILE%\Desktop\run.exe -test.v"