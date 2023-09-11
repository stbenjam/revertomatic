all: build

verify: lint

build:
	go build .

test:
	go test ./...

lint:
	./hack/go-lint.sh run ./...

clean:
	rm -f revertomatic
