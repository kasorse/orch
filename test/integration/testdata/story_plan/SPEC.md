# Partial refunds

## Problem

Refunds are all or nothing.

## Approach

Billing stores the refunded amount; the gateway accepts it.

## Contracts

POST /v1/refunds takes an optional integer amount in cents.

## Out of scope

Reports.

## Risks

—
