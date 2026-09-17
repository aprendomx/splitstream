# SDD ledger — plan: docs/superpowers/plans/2026-09-16-rediseno-panel.md
Spec: docs/superpowers/specs/2026-09-16-rediseno-panel-design.md (leída; autoridad).
Rama: feat/rediseno-panel (spec f8b5181, plan 2fa5b31). Servidor local :8099 para verificación visual.

## Preflight (2026-09-16)
| Par / tarea | Produce vs consume | Hallazgo |
| --- | --- | --- |
| T1 → T2..T7 | tokens `--ss-*`, clases `.ss-t-*`, `.sr-only`, iconos `iDesplegar`… | coherente; `.sr-only` definido en T1 y usado en T2/T3/T6 |
| T1 ↔ T2 | ambos tocan App.vue (T1 solo `:dropdown-icon`; T2 barra completa) | sin conflicto: T2 rehace la barra e incluye `:dropdown-icon` |
| T3 → T5/T6 | `ChipEstado` props tono/icono/texto/tam/pulso | consistentes |
| T3 ↔ diagnostico.js | `TONOS` importado por ChipEstado; T3 «solo si necesita» | ok |
| T4 iconos | usa iVer/iOcultar/iEditar/iFallo/iOk/iCerrar (existen) | ok |
| T5 → T6 | `.cabecera-tarjeta` copiada en Sesion.vue | ok (duplicación deliberada, scoped) |
| T5 VistaPrevia | icono: el que ya usa el panel (iPlayBox) | ok |
| T6 i18n | claves nuevas no colisionan con existentes (`sesion.sin_*` se conservan) | ok |
| T7 barrido | grep excluye `#000` de VistaPrevia | ok |
| Restricciones | sin deps nuevas; Material Icons prohibida; i18n-check | ninguna tarea las contradice |
Rulings preflight: ninguno necesario.

## Tareas
Task 1: dispatched (implementer sonnet, agent af1a9a17c2ef200e1) BASE=2fa5b31
Task 1: implementer DONE_WITH_CONCERNS 0b4661a (.ss-focus añadido al selector de foco; q-stepper con active/done icon SVG — ambas dentro del alcance, aceptadas). Review dispatched (sonnet, ac8d015bc6def0e7a), package review-2fa5b31..0b4661a.diff
Task 1: complete — review Approved (sin hallazgos Important). Nota: tarjetas bordered dentro de q-dialog toman --ss-surface-2 (intencional). HEAD 0b4661a
Task 2: dispatched (implementer sonnet, a3d9252673ab9aa4e) BASE=0b4661a
Task 2: implementer DONE_WITH_CONCERNS 1c2748b (q-route-tab con model-value=route.name por carrera de auto-activación — aceptado; 375px no verificable por el tope de 500px del navegador MCP → lo verifica el controlador en QA con iframe de 375px). Review dispatched.
Task 2: review Needs fixes (Important: reglas móviles scoped sin :deep() → muertas; Minor: nombreIdioma undefined). Fix round 1: resumed implementer a3d9252673ab9aa4e
Task 2: complete — fix round 1 d6fb135 (:deep() + fallback nombreIdioma) re-revisado por el controlador (diff de 3 líneas, correcto). HEAD d6fb135
Task 3: dispatched (implementer sonnet) BASE=d6fb135
Task 3: implementer DONE_WITH_CONCERNS 0b11d47 (reutilizó panel.servidor/clave/copiado/sin_senal en vez de crear duplicados — aceptado; +4 claves de etiqueta; vacío verificado solo por código → QA del controlador). Review dispatched.
Task 3: review Needs fixes (Important: botón copiar clave copiaba key_mask — Ruling: se elimina el botón de copiar en la clave; el brief estaba mal, la clave real solo se revela al rotar — coste si es erróneo: ninguno funcional. Minor: q-tooltip→title, chips sm coloreados a 12px (mandato del brief, se deja), panel.recibiendo_senal huérfana → se borra). Fix round 1: resumed implementer a647a2f66237b31b4
Task 3: complete — fix round 1 a2775a5 re-revisado por el controlador (botón y claves eliminados, comentario explicativo). HEAD a2775a5
Task 4: dispatched (implementer sonnet) BASE=a2775a5
Task 4: implementer DONE_WITH_CONCERNS c508241 (anillo de foco en q-btn suprimido por .no-outline → arreglado por el controlador en base.scss, commit propio; 375px y Conectar cuenta verificados por código → QA del controlador). Review dispatched (a7788fbee7c44c21f) sobre a2775a5..c508241.
Task 4: review Needs fixes (Important: errores de validación no se limpian en elegir(); Minor: clase .codigo-dispositivo inerte). Fix round 1: resumed implementer ad3acedc4fdd75a2e (sobre 6e985df)
Task 4: complete — fix round 1 93d9ad6 re-revisado por el controlador (limpiarErrores() en abrir y elegir; clase muerta fuera). HEAD 93d9ad6
Task 5: dispatched (implementer sonnet) BASE=93d9ad6
Task 5: implementer DONE_WITH_CONCERNS d62876a (Ruling: el chat no tiene entrada de mensaje — error del brief, se omite; icono iVer en vista previa; TituloEnVivo y <600px verificados por código). Review dispatched.
Task 5: complete — review Approved. HEAD d62876a
Task 6: dispatched (implementer sonnet) BASE=d62876a
Task 6: implementer DONE_WITH_CONCERNS bd421b0 (desglose por destino/plataforma de Sesion plegado en KPIs → posible pérdida de información, a juicio del revisor; Ajustes 387 líneas; 375px no capturado). Review dispatched.
Task 6: review Needs fixes (Important: desgloses por destino/plataforma y total de bytes de grabaciones eliminados de Sesion.vue — se restauran bajo los KPI; Minor: título repetido en vacíos de Ajustes). Fix round 1: resumed implementer aecb888ebd52c158e
Task 6: complete — fix round 1 0946575 re-revisado por el controlador (desgloses y bytes restaurados bajo KPIs; vacíos de Ajustes sin título repetido). HEAD 0946575
Task 7: dispatched (implementer sonnet) BASE=0946575
Task 7: implementer cortado por límite de sesión (429) con el árbol a medias; el controlador terminó el barrido y commit 18743fb. Task 7: complete (re-revisión: la hará la revisión final de rama).
QA visual del controlador (iframe 375 y 800 px, mismo origen; capturas screenshot-…-12..16.jpg):
  - Panel, Sesión, Historial, diálogo (paso 1 y 2) correctos a 375 y 800; sin scroll horizontal salvo Ajustes.
  - QA-1 (Important): barra a 375 px desborda (461 px): título «Splitstream» reducido a «S», botón «más» fuera de pantalla. Arreglo: <480 ocultar título, pestañas con padding 4px, idioma sin chevron.
  - QA-2 (Important): índice de Ajustes a 375 px desborda y muestra «chevron_right» en texto (flechas de scroll de q-tabs con fuente). Arreglo: q-tabs con :left-icon/:right-icon SVG (App y Ajustes) + iconos iAnterior/iSiguiente.
  - QA-3 (Minor→Important por spec): botones rectangulares q-btn miden 36 px de alto (spec: ≥44). Arreglo global en base.scss: .q-btn--rectangle:not(.q-btn--dense) { min-height: 44px }.
  - QA-4 (Minor): insignias de funciones en el paso 2 del diálogo (q-badge) muy pequeñas; revisar tamaño 12 px.
  - Anillo de foco en q-btn confirmado (regla compilada y visible en «Cancelar»).
Final review (opus): With fixes — 0 Critical / 9 Important / 17 Minor (final-review.md). Ruling sobre Important 9: la clave nunca tuvo botón de copiar (git 2fa5b31); es un error previo del manual → se corrige el manual. Ola de arreglos única: final-fix-brief.md (A–L = 9 Important + QA-1..4 + Minor triviales), implementer sonnet aad0029672aad6b64, BASE=18743fb
Fix wave: d3d9f31 DONE_WITH_CONCERNS (G resuelto apuntando Historial a la sesión → rechazado por el controlador: b61b37b pasa las pestañas a q-tab + router.push; verificado en iframe: Historial activa en /historial/1, clic vuelve a la lista, barra 360px, flechas SVG en Ajustes). Re-review acotada dispatched sobre 18743fb..HEAD.
QA controlador tras la ola: arrastre con ratón verificado (orden cambia, persiste en /api/destinations y tras recarga; restaurado). Pendiente: veredicto de la re-revisión acotada (a5335091622e5e911).
