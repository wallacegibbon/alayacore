# TLV Request Examples

Generate TLV-encoded image requests for `--rawio` mode.

## Generate a Request

```sh
go run misc/gen_tlv_request.go "your question" image1.jpg [image2.jpg ...]
```

This outputs TLV frames to stdout (redirect to a file or pipe to alayacore).

## Example

```sh
go run misc/gen_tlv_request.go \
  "does the animal in image 2 exist in image1?" \
  misc/samples/tlv-requests/image/dog.jpg \
  misc/samples/tlv-requests/image/hamster.webp \
  | alayacore --rawio
```

Inspect the raw output with `tlvcat`:

```sh
go run misc/gen_tlv_request.go "hi" image.jpg | alayacore --rawio | misc/tlvcat
```

## Pre-built Samples

The `misc/samples/tlv-requests/image/` directory contains sample images and
pre-built request files for quick testing.
