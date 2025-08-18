build:
	@echo "Building the project..."
	go build -o bin/ddg main.go
move:
	@echo "Moving the binary to the /usr/local/bin directory..."
	mv bin/ddg /usr/local/bin/