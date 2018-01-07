FROM umputun/baseimage:buildgo-latest as build

ADD . /go/src/github.com/umputun/docker-logger
WORKDIR /go/src/github.com/umputun/docker-logger

RUN cd app && go test -v $(go list -e ./... | grep -v vendor)

RUN gometalinter --disable-all --deadline=300s --vendor --enable=vet --enable=vetshadow --enable=golint \
    --enable=staticcheck --enable=ineffassign --enable=goconst --enable=errcheck --enable=unconvert \
    --enable=deadcode  --enable=gosimple --enable=gas --exclude=test --exclude=mock --exclude=vendor ./...

RUN mkdir -p target && /script/coverage.sh

RUN go build -o docker-logger -ldflags "-X main.revision=$(git rev-parse --abbrev-ref HEAD)-$(git describe --abbrev=7 --always --tags)-$(date +%Y%m%d-%H:%M:%S) -s -w" ./app


FROM umputun/baseimage:micro-latest

COPY --from=build /go/src/github.com/umputun/docker-logger/docker-logger /srv/
RUN chown -R umputun:umputun /srv

USER umputun

WORKDIR /srv
EXPOSE 8080

CMD ["/srv/docker-logger"]
ENTRYPOINT ["/init.sh"]