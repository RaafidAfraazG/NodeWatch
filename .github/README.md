# Automation

`workflows/ci.yml` runs formatting, vet, unit tests, Linux builds, Compose validation, and a separate real MySQL/Redis integration job. It uses only disposable GitHub Actions service containers and contains no deployment credentials.
