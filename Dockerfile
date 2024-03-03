FROM golang:1.22-alpine

RUN apk update && apk upgrade && \
    apk add --no-cache bash git openssh build-base sqlite

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download && \
    go env -w CGO_ENABLED=1

COPY . .

RUN go build -o main .

EXPOSE 3000

CMD ["./main"]