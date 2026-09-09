export GOFLAGS = -buildvcs=false

.PHONY: build test policies e2e e2e-linux site clean

build:
	go build -o stonewall .

test:
	go test ./...

policies: build
	test/policies.sh ./stonewall

e2e: build
	test/e2e.sh ./stonewall

# Same checks inside a Linux container. bwrap needs mount and user namespaces, hence --privileged.
e2e-linux:
	docker build -q -t stonewall-e2e test
	docker run --rm --privileged -v "$(CURDIR)":/src -v stonewall-gomod:/go/pkg/mod -e GOFLAGS stonewall-e2e \
		sh -c 'go test ./... && go build -o /tmp/stonewall . && test/e2e.sh /tmp/stonewall'

# Website preview at http://localhost:1313. site/ mounts README.md and policies/ from the root.
site:
	hugo server --source site

clean:
	rm -f stonewall
