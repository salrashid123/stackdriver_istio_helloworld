FROM golang:1.13 AS build
WORKDIR /src/be
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go install -a -tags netgo -ldflags=-w

FROM gcr.io/distroless/base
ARG VER=0
ENV VER $VER
COPY --from=build /go/bin/be /bin/be

EXPOSE 8080
ENTRYPOINT [ "/bin/be" ]
