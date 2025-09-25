configure:
	go mod tidy

run:
	go run main.go

build:
	sudo docker build --platform linux/amd64 -t kirari04/cloudconv:latest --push .