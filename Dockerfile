# Build the Go app
FROM golang:latest AS build
WORKDIR /app
COPY . /app
RUN go build -o main .

# Build the scratch image
FROM scratch
COPY --from=build /app/main /app/main
LABEL "tag"="what-i-see:latest"
ENTRYPOINT  ["/app/main"]