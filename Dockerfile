FROM golang:1.22 AS build
WORKDIR /app

# só copia go.mod
COPY go.mod . 

# baixa dependências iniciais
RUN go mod download

# copia resto do código
COPY . .

# gera go.sum se não existir
RUN go mod tidy

# compila
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o api

FROM gcr.io/distroless/base-debian12
WORKDIR /
COPY --from=build /app/api /api
ENV APP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/api"]
