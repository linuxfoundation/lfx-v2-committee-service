# lfx-v2-committee-service

This repository contains the source code for the LFX v2 platform committee service.

## Development

### Prerequisites

This project uses the [GOA Framework](https://goa.design/) for API generation. You'll need to install GOA before building the project.

#### Installing GOA Framework

Follow the [GOA installation guide](https://goa.design/docs/2-getting-started/1-installation/) to install GOA:

```bash
go install goa.design/goa/v3/cmd/goa@latest
```

Verify the installation:

```bash
goa version
```

### Building and Development

#### 1. Generate Code

The project uses GOA to generate API code from the design specification. Run the following command to generate all necessary code:

```bash
goa gen github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-api/design
```

This command generates:

- HTTP server and client code
- OpenAPI specification
- Service interfaces and types
- Transport layer implementations

#### 2. Initial Project Structure

**Note**: The initial `cmd` structure was generated using GOA's example generator:

```bash
goa example github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-api/design
```

This command generated the basic server structure, which was then customized and adjusted to fit our project's clean architecture principles.

#### 3. Development Workflow

1. **Make design changes**: Edit files in the `cmd/committee-api/design/` directory
2. **Regenerate code**: Run `goa gen github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-api/design` after design changes
3. **Build the project**:

   ```bash
   go build cmd
   ```

4. **Run the service**:

   ```bash
   go run ./cmd
   ```

5. **Run tests**:

   ```bash
   go test ./...
   ```

### Contributing

To contribute to this repository:

1. Fork the repository
2. Make your changes
3. Submit a pull request

## License

Copyright The Linux Foundation and each contributor to LFX.

This project's source code is licensed under the MIT License. A copy of the
license is available in `LICENSE`.

This project's documentation is licensed under the Creative Commons Attribution
4.0 International License \(CC-BY-4.0\). A copy of the license is available in
`LICENSE-docs`.
