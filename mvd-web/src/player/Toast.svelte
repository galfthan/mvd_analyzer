<script>
    import { toastText } from './toast.js';
</script>

<div class="player-toast" class:show={!!$toastText}>{$toastText}</div>
