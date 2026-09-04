# Separate lifecycle, requirement, outcome, and dispatch state

Factory records Issue lifecycle status, closure outcome, requirement classification, and derived dispatch state as separate concepts. This avoids treating closure as success or dependency waiting as lifecycle, permits conditional failure branches and accurate progress, and accepts a more explicit transition model in exchange for preventing ambiguous scheduling behavior.
