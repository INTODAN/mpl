# The lobby's own name: players and games reach https://<lobby_hostname>,
# which a CloudFront distribution serves from the lobby's Function URL. A DNS
# record alone can't do it: a Function URL's certificate only covers
# *.lambda-url.<region>.on.aws, and it expects its own Host. DNS is outside
# AWS, so the certificate's validation record and the name's CNAME are added
# by hand, from the outputs (deploy/README.md).

locals {
  # https://<id>.lambda-url.<region>.on.aws/ without the scheme and the slash.
  function_url_host = split("/", aws_lambda_function_url.lobby.function_url)[2]
}

# CloudFront only takes certificates from us-east-1. The first apply requests
# it on its own, with -target, so its validation record can be added before
# anything waits for it (deploy/README.md). ACM renews the certificate only
# while that record stays in place.
resource "aws_acm_certificate" "lobby" {
  region            = "us-east-1"
  domain_name       = var.lobby_hostname
  validation_method = "DNS"

  lifecycle {
    # A new name gets its certificate before the distribution lets go of the
    # old one.
    create_before_destroy = true
  }
}

# Waits until ACM has seen the validation record and issued the certificate.
resource "aws_acm_certificate_validation" "lobby" {
  region          = "us-east-1"
  certificate_arn = aws_acm_certificate.lobby.arn
}

data "aws_cloudfront_cache_policy" "caching_disabled" {
  name = "Managed-CachingDisabled"
}

# Caches an answer for as long as the origin's Cache-Control says, and not at
# all without one or with no-store. The managed UseOriginCacheControlHeaders
# policy does the same but puts the viewer's Host header and every cookie in
# the cache key, and CloudFront sends cache-key headers to the origin: a
# Function URL refuses a request whose Host isn't its own. So this policy
# keys on the path alone (and the encoding CloudFront compresses to).
resource "aws_cloudfront_cache_policy" "origin_cache_control" {
  name        = "${var.name}-origin-cache-control"
  comment     = "Follows the origin's Cache-Control; the cache key is the path."
  min_ttl     = 0
  default_ttl = 0
  max_ttl     = 31536000

  parameters_in_cache_key_and_forwarded_to_origin {
    enable_accept_encoding_gzip   = true
    enable_accept_encoding_brotli = true

    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
  }
}

# Every header, cookie and query string except Host, which the Function URL
# needs to be its own. CloudFront strips Authorization from GET and HEAD
# requests unless the policy forwards all viewer headers, so the smoke test
# checks an authenticated GET.
data "aws_cloudfront_origin_request_policy" "all_viewer_except_host_header" {
  name = "Managed-AllViewerExceptHostHeader"
}

# Except for the lobby page's files (below), nothing is cached: every request
# reaches the Lambda as if it came straight to the Function URL. There's no
# origin access control. For a Function URL it would need clients to send a
# SHA-256 of every POST body, and the Function URL is public anyway. Standard
# and real-time logs stay off.
resource "aws_cloudfront_distribution" "lobby" {
  enabled         = true
  comment         = "${var.name}: the lobby Lambda at ${var.lobby_hostname}"
  aliases         = [var.lobby_hostname]
  is_ipv6_enabled = true
  # North America, Europe, Israel and Türkiye: the cheapest edges, should the
  # free tier ever run out. The origin is in one region, and little is
  # cached, so further edges would gain little.
  price_class = "PriceClass_100"

  origin {
    origin_id   = "lobby"
    domain_name = local.function_url_host

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
      # How long CloudFront waits for the first byte, and between the parts of
      # a streamed answer. The most it allows without a quota increase. A GET
      # that runs out of it would be retried, so lambda_timeout_seconds stays
      # under it with room for a cold start.
      origin_read_timeout = 60
    }
  }

  # The lobby page (frontend/): its handler says in Cache-Control what may be
  # kept, scripts and styles for a few minutes, the HTML and the game catalog
  # not at all, so this policy obeys the origin. No API route lies under this
  # prefix, so API answers stay uncached.
  ordered_cache_behavior {
    path_pattern             = "/lobby/ui/*"
    target_origin_id         = "lobby"
    viewer_protocol_policy   = "redirect-to-https"
    allowed_methods          = ["GET", "HEAD", "OPTIONS"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = aws_cloudfront_cache_policy.origin_cache_control.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer_except_host_header.id
    compress                 = true
  }

  default_cache_behavior {
    target_origin_id         = "lobby"
    viewer_protocol_policy   = "redirect-to-https"
    allowed_methods          = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer_except_host_header.id
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.lobby.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }
}
