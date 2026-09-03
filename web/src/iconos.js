// Iconos SVG importados de uno en uno.
//
// La alternativa —importar el CSS de mdi-v7— mete la fuente ENTERA en el binario: 394 KB
// de woff2 más 574 KB de woff para usar una docena de glifos. Este panel se sirve a un
// móvil, a veces con mala cobertura, a mitad de una transmisión.
//
// Quasar acepta el propio SVG como `name`, así que se usan igual: <q-icon :name="iBroadcast" />
export {
  mdiBroadcast as iBroadcast,
  mdiClose as iCerrar,
  mdiPlus as iMas,
  mdiPencil as iEditar,
  mdiDelete as iBorrar,
  mdiKey as iClave,
  mdiEye as iVer,
  mdiEyeOff as iOcultar,
  mdiDotsVertical as iMenu,
  mdiDragVertical as iArrastrar,
  mdiContentCopy as iCopiar,
  mdiRefresh as iRotar,
  mdiLogout as iSalir,
  mdiCheckCircle as iOk,
  mdiAlert as iAviso,
  mdiCloseCircle as iFallo,
  mdiCircleOutline as iNeutro,
  mdiSync as iTrabajando,
  mdiInformationOutline as iInfo,
  mdiAlertCircle as iError,
  mdiLightbulbOutline as iConsejo,
  mdiYoutube as iYoutube,
  mdiTwitch as iTwitch,
  mdiFacebook as iFacebook,
  mdiPlayBox as iKick,
  mdiAlphaXBox as iX,
  mdiMusicNote as iTiktok,
  mdiServerNetwork as iServidor,
} from '@quasar/extras/mdi-v7'
