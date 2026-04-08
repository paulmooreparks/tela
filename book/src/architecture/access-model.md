# The access model

This chapter is the design rationale for Tela's token-based role-based access control: what problem the model was designed to solve, why the project chose four roles instead of more or fewer, why the unified `/api/admin/access` endpoint joins tokens and per-machine permissions into a single resource, and what the alternatives looked like.

{{#include ../../../ACCESS-MODEL.md:3:}}
