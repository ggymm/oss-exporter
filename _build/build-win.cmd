del /q out
cd ..
set GOARCH=386
go build -ldflags "-s -w" -o _build\release\