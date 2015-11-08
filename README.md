# Bonsai
Bonsai is a massive vegetation evolution simulator. Things below are just plans and not implemented.

Bonsai simulates plants at individual cell level, and has well-defined physics for lighting, sofy-body, etc..
All physics is local and energy / mass is carefully conserved.

## Simulated Aspects
* soft-body (particle-based)
* light (ray-traced)
* fake chemistry
* cell cycle & division
* DNA - protein relationship

All of these affects each other, and all interactions are localized and completely well-defined like real world.
This is super important property since evolving things tend to *cheat* by exploiting undefined edge cases
(e.g. infinite efficiency by creating infinitely thin cell).

## Infra
World is divided to re-sizable chunks and they're simulated in parallel on google cloud platform.
Simulation is bit-wise reproducible when run in any chunk size and/or in presence of server failures.

## About Performance
At 6d177626e0dddd23fbd9039e7945aa899e51eea1
(300 water particles & 300 soil particles, https://gyazo.com/9e414fc8ba2fecb76a08528d013c24c5),

Measured time for simulating 1200 steps.

Average of 3 measurements:
* javascript (`client/biosphere.js`): 12.3sec
* go (`chunk/service.go`): 1.9sec

I did confirm these two results in visually identical simulation.

### Micro optimization in go
* math.Pow -> custom int pow: very effective
* map -> array: very effective
* pre-allocation of slice: moderately effective if cap is known
* Mutable pointer-based Vec3f (like three.js): slower than value-passing, even with some hand-optimization of equations to make them in-place
