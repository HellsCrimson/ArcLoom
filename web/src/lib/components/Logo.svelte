<!--
  Logo — the ArcLoom mark, inlined so it inherits `currentColor` (an <img src>
  cannot, which is the whole reason brand/arcloom-mark-mono exists as a separate
  file). Geometry is copied from brand/arcloom-mark-solid.svg: the SMALL-SIZE
  variant, because every in-app use is 14-24 px, where the twist seams alias into
  mush. The interlace — the cord passing under the A's right leg — is what still
  reads at that size, and it is kept.

  Edit brand/generate.py and re-run it if the geometry changes; this is a copy,
  not a build artefact.

  The mask id MUST be per-instance: the header, the sidebar and the login card
  can all be on screen at once, and duplicate ids in one document make every
  copy use whichever mask the browser resolved first.
-->
<script lang="ts">
	let {
		size = 18,
		class: cls = ''
	}: { size?: number; class?: string } = $props();

	const uid = $props.id();
	const maskId = `al-mark-${uid}`;
</script>

<svg
	viewBox="0 0 64 64"
	width={size}
	height={size}
	class={cls}
	role="img"
	aria-label="ArcLoom"
	fill="none"
	stroke="currentColor"
	stroke-linecap="round"
	stroke-linejoin="round"
>
	<defs>
		<mask id={maskId} maskUnits="userSpaceOnUse" x="0" y="0" width="64" height="64">
			<rect width="64" height="64" fill="#fff" />
			<!-- The right leg stroked 7.5 + 2×1.3 wide: uniform perpendicular
			     clearance where the cord dives under it. -->
			<path d="M34 19 L47 55" stroke="#000" stroke-width="10.1" />
		</mask>
	</defs>
	<!-- The A: vertical left leg (which is also the L's stem), apex arc, splayed
	     right leg. -->
	<path d="M14 55 L14 19 A10 10 0 0 1 34 19 L47 55" stroke-width="7.5" />
	<!-- The cord: the A's crossbar, the L's foot, and a hook for the loose end. -->
	<path d="M4 37 H51.37 Q57.37 37 57.37 28" stroke-width="6.5" mask="url(#{maskId})" />
</svg>
